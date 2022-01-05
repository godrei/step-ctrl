package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/v2/env"
)

// Config ...
type Config struct {
	RepositoryURL   string `env:"repository_url,required"`
	RepositoryOwner string `env:"repository_owner,required"`
	TriggerToken    string `env:"trigger_token,required"`
	APIToken        string `env:"api_token,required"`
	AppSlug         string `env:"app_slug,required"`
	StackID         string `env:"stack_id,required"`
	MachineType     string `env:"machine_type,required"`
	Workflow        string `env:"workflow,required"`
	Envs            string `env:"envs"`
	HangTimeoutSec  int    `env:"hang_timeout,required"`
	HangWebhookURL  string `env:"hang_webhook,required"`
	HangChannel     string `env:"hang_channel,required"`
}

func main() {
	if err := runController(); err != nil {
		log.Errorf("%s", err)
		os.Exit(1)
	}
}

func runController() error {
	var conf Config
	parser := stepconf.NewInputParser(env.NewRepository())
	if err := parser.Parse(&conf); err != nil {
		return err
	}
	stepconf.Print(conf)

	envs := map[string]string{
		"GIT_REPOSITORY_URL": conf.RepositoryURL,
	}

	s := strings.Split(conf.Envs, "\n")
	for _, e := range s {
		es := strings.Split(e, "=")
		envs[es[0]] = es[1]
	}

	key := Key{
		Stack:       conf.StackID,
		MachineType: conf.MachineType,
		Workflow:    conf.Workflow,
		ID:          fmt.Sprintf("%s [%s]", conf.StackID, conf.MachineType),
		Envs:        envs,
		RepoOwner:   conf.RepositoryOwner,
	}
	hangingBuildWarning := HangingBuildWarning{
		Timeout:    time.Duration(conf.HangTimeoutSec) * time.Second,
		WebhookURL: conf.HangWebhookURL,
		Channel:    conf.HangChannel,
	}
	if _, err := ExecuteWorkflows(conf.TriggerToken, conf.APIToken, conf.AppSlug, key, hangingBuildWarning); err != nil {
		return err
	}

	return nil
}
