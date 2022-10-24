package main

import (
	"fmt"
	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/v2/env"
	"os"
	"strings"
)

// Config ...
type Config struct {
	TriggerToken string `env:"trigger_token,required"`
	APIToken     string `env:"api_token,required"`
	AppSlug      string `env:"app_slug,required"`
	StackID      string `env:"stack_id,required"`
	MachineType  string `env:"machine_type,required"`
	Workflow     string `env:"workflow,required"`
	EnvMatrix    string `env:"env_matrix"`
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

	envMatrix, err := parseEnvMatrix(conf.EnvMatrix)
	if err != nil {
		return err
	}

	var keys []Key
	for _, envs := range envMatrix {
		keys = append(keys, Key{
			Stack:       conf.StackID,
			MachineType: conf.MachineType,
			Workflow:    conf.Workflow,
			ID:          calculateBuildID(conf.StackID, conf.MachineType, envs),
			Envs:        envs,
		})
	}

	if _, err := ExecuteWorkflows(conf.TriggerToken, conf.APIToken, conf.AppSlug, keys); err != nil {
		return err
	}

	return nil
}

func parseEnvMatrix(matrixStr string) ([]map[string]string, error) {
	var matrix []map[string]string

	split := strings.Split(matrixStr, "\n")
	for _, line := range split {
		if line == "" {
			continue
		}

		envs := map[string]string{}

		fields := strings.Split(line, ",")
		for _, field := range fields {
			fieldSplit := strings.Split(field, "=")
			if len(fieldSplit) != 2 {
				return nil, fmt.Errorf("invalid env matrix field: %s", field)
			}
			key := fieldSplit[0]
			value := fieldSplit[1]

			envs[key] = value
		}

		matrix = append(matrix, envs)
	}

	return matrix, nil
}

func calculateBuildID(stackID, machineType string, envs map[string]string) string {
	var keyValues []string
	for key, value := range envs {
		keyValues = append(keyValues, key+"="+value)
	}
	keyValuesStr := strings.Join(keyValues, ",")
	return fmt.Sprintf("%s [%s] - %s", stackID, machineType, keyValuesStr)
}
