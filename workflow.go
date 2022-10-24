package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexeyco/simpletable"
	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pretty"
	"github.com/bitrise-io/go-utils/v2/env"
)

// Key ...
type Key struct {
	Stack       string
	MachineType string
	Workflow    string
	ID          string
	Envs        map[string]string
	RepoOwner   string
}

type buildKey struct {
	triggerResult BuildTriggerResponse
	key           Key
}

// BuildTriggerResponse ...
type BuildTriggerResponse struct {
	Status    string `json:"status"`
	AppSlug   string `json:"slug"`
	BuildSlug string `json:"build_slug"`
}

// BuildTriggerParams ...
type BuildTriggerParams struct {
	HookInfo    BuildTriggerParamsHookInfo    `json:"hook_info"`
	BuildParams BuildTriggerParamsBuildParams `json:"build_params"` // The public part of the SSH key you would like to use
}

// BuildTriggerParamsHookInfo ...
type BuildTriggerParamsHookInfo struct {
	Type              string `json:"type" example:"bitrise"` // Should be "bitrise"
	BuildTriggerToken string `json:"build_trigger_token"`
}

// BuildTriggerParamsBuildParams ...
type BuildTriggerParamsBuildParams struct {
	CommitHash               string                   `json:"commit_hash" env:"BITRISE_GIT_COMMIT"`
	CommitMessage            string                   `json:"commit_message" env:"BITRISE_GIT_MESSAGE"`
	Tag                      string                   `json:"tag" env:"BITRISE_GIT_TAG"`
	Branch                   string                   `json:"branch" env:"BITRISE_GIT_BRANCH"`
	BranchRepoOwner          string                   `json:"branch_repo_owner" env:"BITRISEIO_GIT_REPOSITORY_OWNER"`
	BranchDest               string                   `json:"branch_dest" env:"BITRISEIO_GIT_BRANCH_DEST"`
	BranchDestRepoOwner      string                   `json:"branch_dest_repo_owner" env:"BITRISEIO_GIT_REPOSITORY_OWNER"`
	PullRequestID            int                      `json:"pull_request_id" env:"PULL_REQUEST_ID"`
	PullRequestRepositoryURL string                   `json:"pull_request_repository_url" env:"BITRISEIO_PULL_REQUEST_REPOSITORY_URL"`
	PullRequestMergeBranch   string                   `json:"pull_request_merge_branch" env:"BITRISEIO_PULL_REQUEST_MERGE_BRANCH"`
	PullRequestHeadBranch    string                   `json:"pull_request_head_branch" env:"BITRISEIO_PULL_REQUEST_HEAD_BRANCH"`
	WorkflowID               string                   `json:"workflow_id"`
	SkipGitStatusReport      bool                     `json:"skip_git_status_report"`
	Environments             []BuildParamsEnvironment `json:"environments"`
	Worker                   struct {
		StackID       string `json:"only_with_stack_id"`
		MachineTypeID string `json:"machine_type"`
	} `json:"worker"`
}

// BuildParamsEnvironment ...
type BuildParamsEnvironment struct {
	MappedTo string `json:"mapped_to"`
	Value    string `json:"value"`
	IsExpand bool   `json:"is_expand"`
}

// ExecuteWorkflows ...
func ExecuteWorkflows(triggerToken string, apiToken string, appSlug string, keys []Key) (map[string]BuildInfo, error) {
	fmt.Println()
	log.Infof("Trigger Workflows")

	var startedBuilds []buildKey
	for _, key := range keys {
		startedBuild, err := triggerWorkflows(triggerToken, appSlug, key)
		if err != nil {
			return nil, err
		}

		startedBuilds = append(startedBuilds, *startedBuild)
	}

	fmt.Println()
	log.Infof("Monitoring Workflows")

	buildInfos := map[string]BuildInfo{}
	for _, startedBuild := range startedBuilds {
		buildInfo, err := monitorRunningBuild(apiToken, startedBuild)
		if err != nil {
			return nil, err
		}

		buildInfos[startedBuild.key.ID] = buildInfo
	}

	printBuildInfos(buildInfos)

	return buildInfos, nil
}

func triggerWorkflows(triggerToken, appSlug string, key Key) (*buildKey, error) {
	log.Printf("Starting %s", key.ID)
	params, err := newBuildTriggerParams(key, triggerToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create buildparams: %s", err)
	}

	// TODO investigate issue with several commit messages, then remove
	params.BuildParams.CommitMessage = ""

	if key.RepoOwner != "" {
		params.BuildParams.BranchRepoOwner = key.RepoOwner
		params.BuildParams.BranchDestRepoOwner = key.RepoOwner
	}

	log.Printf("Params:\n%s", pretty.Object(params))

	url := "https://app.bitrise.io/app/" + appSlug + "/build/start.json"

	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 201 {
		var bodyStr string
		b, err := ioutil.ReadAll(resp.Body)
		if err == nil {
			bodyStr = string(b)
		}

		if len(bodyStr) != 0 {
			return nil, fmt.Errorf("http response %s: %s", resp.Status, bodyStr)
		}
		return nil, fmt.Errorf("http response %s", resp.Status)
	}

	var triggerResp BuildTriggerResponse

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &triggerResp); err != nil {
		return nil, err
	}

	log.Printf("Trigger response: %+v", triggerResp)

	if triggerResp.Status != "ok" {
		return nil, fmt.Errorf("build trigger response (%s) is not 'ok'", triggerResp.Status)
	}

	return &buildKey{
		triggerResult: triggerResp,
		key:           key,
	}, nil
}

func monitorRunningBuild(apiToken string, startedBuild buildKey) (BuildInfo, error) {
	var buildInfo BuildInfo
	var mux sync.Mutex

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg.Add(1)

	var buildErr error

	appSlug := startedBuild.triggerResult.AppSlug
	buildSlug := startedBuild.triggerResult.BuildSlug
	id := startedBuild.key.ID
	go func() {
		defer wg.Done()
		build, err := pollBuild(ctx, apiToken, appSlug, buildSlug, id)
		if err != nil {
			buildErr = err
			cancel()
		}
		mux.Lock()
		buildInfo = build
		mux.Unlock()
	}()

	wg.Wait()

	return buildInfo, buildErr
}

// BuildInfo ...
type BuildInfo struct {
	Status    string
	RawStatus string
	URL       string
	ID        string
	Duration  string
}

func printBuildInfos(buildInfos map[string]BuildInfo) {
	table := simpletable.New()

	table.Header = &simpletable.Header{
		Cells: []*simpletable.Cell{
			{Align: simpletable.AlignCenter, Text: "ID"},
			{Align: simpletable.AlignCenter, Text: "DURATION"},
			{Align: simpletable.AlignCenter, Text: "URL"},
			{Align: simpletable.AlignCenter, Text: "STATUS"},
		},
	}
	for _, buildInfo := range buildInfos {
		r := []*simpletable.Cell{
			{Text: buildInfo.ID},
			{Text: buildInfo.Duration},
			{Text: buildInfo.URL},
			{Align: simpletable.AlignRight, Text: buildInfo.Status},
		}

		table.Body.Cells = append(table.Body.Cells, r)
	}
	table.SetStyle(simpletable.StyleUnicode)
	fmt.Println(table.String())
	fmt.Println()
}

func newBuildTriggerParams(key Key, triggerToken string) (BuildTriggerParams, error) {
	var params BuildTriggerParams
	params.HookInfo.BuildTriggerToken = triggerToken

	parser := stepconf.NewInputParser(env.NewRepository())
	if err := parser.Parse(&params.BuildParams); err != nil {
		return BuildTriggerParams{}, err
	}

	params.HookInfo.Type = "bitrise"

	for key, value := range key.Envs {
		params.BuildParams.Environments = append(params.BuildParams.Environments, BuildParamsEnvironment{
			MappedTo: key,
			Value:    value,
			IsExpand: true,
		})
	}

	if key.Stack != "" {
		params.BuildParams.Worker.StackID = key.Stack
	}
	if key.MachineType != "" {
		params.BuildParams.Worker.MachineTypeID = key.MachineType
	}
	params.BuildParams.WorkflowID = key.Workflow
	params.BuildParams.SkipGitStatusReport = true

	return params, nil
}

// Build statuses
const (
	StatusOnHold = "on-hold" // 0 + IsOnHold is true
	// StatusInProgress ...
	StatusInProgress = "in-progress" // 0
	// StatusFinishedWithSuccess ...
	StatusFinishedWithSuccess = "success" // 1
	// StatusFinishedWithError ...
	StatusFinishedWithError = "error" // 2
	// StatusAborted ...
	StatusAborted = "aborted" // 3
	// StatusAbortedWithSuccess ...
	StatusAbortedWithSuccess = "aborted-with-success" // 4
	// StatusUnknown ...
	StatusUnknown = "unknown"
)

type HangingBuildWarning struct {
	Timeout    time.Duration
	WebhookURL string
	Channel    string
}

func pollBuild(ctx context.Context, apiToken string, appSlug string, buildSlug string, id string) (BuildInfo, error) {
	for {
		build, err := GetBuild(apiToken, appSlug, buildSlug)
		if err != nil {
			fmt.Println()
			log.Errorf("[%s] Failed to get build: %s", id, err)
			time.Sleep(10 * time.Second)
			continue
		}
		duration := calculateDuration(build)

		switch build.StatusText {
		case StatusOnHold, StatusInProgress:
			fmt.Print(colorstring.NoColor("."))
			time.Sleep(time.Second * 10)
			continue
		case StatusFinishedWithSuccess:
			fmt.Print(colorstring.Green("."))
			return getBuildInfo(id, buildSlug, build.StatusText, colorstring.Green(build.StatusText), duration), nil
		case StatusFinishedWithError, StatusAborted, StatusAbortedWithSuccess, StatusUnknown:
			err := getBuildError(id, build.StatusText)
			var buildInfo BuildInfo
			switch build.StatusText {
			case StatusFinishedWithError:
				fmt.Print(colorstring.Red("."))
				buildInfo = getBuildInfo(id, buildSlug, build.StatusText, colorstring.Red(build.StatusText), duration)
				select {
				case <-ctx.Done():
					return buildInfo, nil
				default:
					return buildInfo, err
				}

			case StatusAbortedWithSuccess:
				fmt.Print(colorstring.Yellow("."))
				buildInfo = getBuildInfo(id, buildSlug, build.StatusText, colorstring.Yellow(build.StatusText), duration)
				select {
				case <-ctx.Done():
					return buildInfo, nil
				default:
					return buildInfo, err
				}

			case StatusUnknown:
				fmt.Print(colorstring.Blue("."))
				buildInfo = getBuildInfo(id, buildSlug, build.StatusText, colorstring.Blue(build.StatusText), duration)
				select {
				case <-ctx.Done():
					return buildInfo, nil
				default:
					return buildInfo, err
				}

			case StatusAborted:
				fmt.Print(colorstring.Yellow("."))
				buildInfo = getBuildInfo(id, buildSlug, build.StatusText, colorstring.Yellow(build.StatusText), duration)
				select {
				case <-ctx.Done():
					return buildInfo, nil
				default:
					return buildInfo, err
				}
			}
		}
	}
}

func abortBuilds(apiToken string, appSlug string, buildSlug string, id string) string {
	_, err := AbortBuild(apiToken, appSlug, buildSlug)
	if err != nil {
		return fmt.Sprintf("[%s] Failed to abort build: %s", id, strings.TrimSpace(err.Error()))
	}
	return fmt.Sprintf("[%s] Build aborted", id)
}

func calculateDuration(build Build) string {
	if (build.StartedOnWorkerAt != nil || build.TriggeredAt != nil) && build.FinishedAt != nil {
		var st string
		if build.StartedOnWorkerAt != nil {
			st = *build.StartedOnWorkerAt
		} else if build.TriggeredAt != nil {
			st = *build.TriggeredAt
		}
		et := *build.FinishedAt
		startTime, err := time.Parse(time.RFC3339, st)
		if err == nil {
			endTime, err := time.Parse(time.RFC3339, et)
			if err == nil {
				return endTime.Sub(startTime).String()
			}
		}
	}
	return "unknown"
}

func getBuildError(id string, statusText string) error {
	return fmt.Errorf("[%s] %s", id, statusText)
}

func getBuildInfo(id string, buildSlug string, status string, statusText string, durationText string) BuildInfo {
	return BuildInfo{
		RawStatus: status,
		Status:    statusText,
		URL:       "https://app.bitrise.io/build/" + buildSlug,
		ID:        id,
		Duration:  durationText,
	}
}

// BuildOriginalBuildParams ...
type BuildOriginalBuildParams struct {
	Branch, WorkflowID string
	Envrironments      []BuildParamsEnvironment `json:"environments"`
}

// Build ...
type Build struct {
	StartedOnWorkerAt   *string                  `json:"started_on_worker_at"`
	TriggeredAt         *string                  `json:"triggered_at"` //"2020-03-18T00:00:09Z" "null"
	FinishedAt          *string                  `json:"finished_at"`
	IsOnHold            bool                     `json:"is_on_hold"` // true
	Slug                string                   `json:"slug"`       // "e0e82f53d9b2588e",
	BuildNumber         int64                    `json:"build_number"`
	Status              int64                    `json:"status"`           //  0,
	StatusText          string                   `json:"status_text"`      // "on-hold",
	AbortReason         *string                  `json:"abort_reason"`     // null,
	MachineTypID        string                   `json:"machine_type_id"`  // "standard",
	StackIdentifier     string                   `json:"stack_identifier"` // "osx-xcode-11.3.x",
	OriginalBuildParams BuildOriginalBuildParams `json:"original_build_params"`
}

const (
	baseURL = "https://api.bitrise.io/v0.1"
)

// GetBuild ...
func GetBuild(personalAccessToken, appSlug, buildSlug string) (Build, error) {
	url := fmt.Sprintf("%s/apps/%s/builds/%s", baseURL, appSlug, buildSlug)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Build{}, fmt.Errorf("failed to construct get build request (URL: %s): %s", url, err)
	}

	req.Header.Add("Authorization", fmt.Sprintf("token %s", personalAccessToken))
	req.Header.Add("Content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Build{}, err
	}

	defer func() {
		cErr := resp.Body.Close()
		if cErr != nil {
			log.Warnf("Failed to close response body: %s", err)
		}
	}()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Build{}, fmt.Errorf("failed to read get build request response: %s", err)
	}

	if resp.StatusCode != 200 {
		return Build{}, fmt.Errorf("HTTP response code (%d %s) not 200, response: %s", resp.StatusCode, resp.Status, data)
	}

	m := struct {
		Data Build `json:"data"`
	}{}

	err = json.Unmarshal(data, &m)
	if err != nil {
		return Build{}, fmt.Errorf("failed to unmarshal get build response: %s", err)
	}

	return m.Data, nil
}

// BuildAbortResponse ...
type BuildAbortResponse struct {
	Status string `json:"status"`
}

// AbortBuild ...
func AbortBuild(personalAccessToken, appSlug, buildSlug string) (BuildAbortResponse, error) {
	url := fmt.Sprintf("%s/apps/%s/builds/%s/abort", baseURL, appSlug, buildSlug)

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return BuildAbortResponse{}, fmt.Errorf("failed to construct get build request (URL: %s): %s", url, err)
	}

	req.Header.Add("Authorization", fmt.Sprintf("token %s", personalAccessToken))
	req.Header.Add("Content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return BuildAbortResponse{}, err
	}

	defer func() {
		cErr := resp.Body.Close()
		if cErr != nil {
			log.Warnf("Failed to close response body: %s", err)
		}
	}()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return BuildAbortResponse{}, fmt.Errorf("failed to read get build request response: %s", err)
	}

	if resp.StatusCode != 200 {
		return BuildAbortResponse{}, fmt.Errorf("HTTP response code (%d %s) not 200, response: %s", resp.StatusCode, resp.Status, data)
	}

	var response BuildAbortResponse

	err = json.Unmarshal(data, &response)
	if err != nil {
		return BuildAbortResponse{}, fmt.Errorf("failed to unmarshal get build response: %s", err)
	}

	return response, nil
}
