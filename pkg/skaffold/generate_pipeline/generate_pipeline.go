/*
Copyright 2019 The Skaffold Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package generatepipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"

	tekton "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"

	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/pipeline"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/schema/latest"
)

// ConfigFile keeps track of config files and their corresponding SkaffoldConfigs and generated Profiles
type ConfigFile struct {
	Path    string
	Config  *latest.SkaffoldConfig
	Profile *latest.Profile
}

func Yaml(out io.Writer, configFiles []*ConfigFile) (*bytes.Buffer, error) {
	// Generate git resource for pipeline
	gitResource, err := generateGitResource()
	if err != nil {
		return nil, errors.Wrap(err, "generating git resource for pipeline")
	}

	// Generate build task for pipeline
	var tasks []*tekton.Task
	buildTasks, err := generateBuildTasks(configFiles)
	if err != nil {
		return nil, errors.Wrap(err, "generating build task")
	}
	tasks = append(tasks, buildTasks...)

	// Generate deploy task for pipeline
	deployTasks, err := generateDeployTasks(configFiles)
	if err != nil {
		return nil, errors.Wrap(err, "generating deploy task")
	}
	tasks = append(tasks, deployTasks...)

	// Generate pipeline from git resource and tasks
	pipeline, err := generatePipeline(tasks)
	if err != nil {
		return nil, errors.Wrap(err, "generating tekton pipeline")
	}

	// json.Marshal all pieces of pipeline, then convert all jsons to yamls
	var jsons [][]byte
	bGitResource, err := json.Marshal(gitResource)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling git resource")
	}
	jsons = append(jsons, bGitResource)
	for _, task := range tasks {
		bTask, err := json.Marshal(task)
		if err != nil {
			return nil, errors.Wrap(err, "marshaling task")
		}
		jsons = append(jsons, bTask)
	}
	bPipeline, err := json.Marshal(pipeline)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling pipeline")
	}
	jsons = append(jsons, bPipeline)

	output := bytes.NewBuffer([]byte{})
	for _, item := range jsons {
		itemYaml, err := yaml.JSONToYAML(item)
		if err != nil {
			return nil, errors.Wrap(err, "converting jsons to yamls")
		}
		output.Write(append(itemYaml, []byte("---\n")...))
	}
	return output, nil
}

func generateGitResource() (*tekton.PipelineResource, error) {
	// Get git repo url
	gitURL := os.Getenv("PIPELINE_GIT_URL")
	if gitURL == "" {
		getGitRepo := exec.Command("git", "config", "--get", "remote.origin.url")
		bGitRepo, err := getGitRepo.Output()
		if err != nil {
			return nil, errors.Wrap(err, "getting git repo from git config")
		}
		gitURL = string(bGitRepo)
	}

	return pipeline.NewGitResource("source-git", gitURL), nil
}

func generatePipeline(tasks []*tekton.Task) (*tekton.Pipeline, error) {
	if len(tasks) == 0 {
		return nil, errors.New("no tasks to add to pipeline")
	}

	resources := []tekton.PipelineDeclaredResource{
		{
			Name: "source-repo",
			Type: tekton.PipelineResourceTypeGit,
		},
	}
	// Create tasks in pipeline spec for all corresponding tasks
	pipelineTasks := make([]tekton.PipelineTask, 0)
	for i, task := range tasks {
		pipelineTask := tekton.PipelineTask{
			Name: fmt.Sprintf("%s-task", task.Name),
			TaskRef: tekton.TaskRef{
				Name: task.Name,
			},
			Resources: &tekton.PipelineTaskResources{
				Inputs: []tekton.PipelineTaskInputResource{
					{
						Name:     "source",
						Resource: "source-repo",
					},
				},
			},
		}
		// Add output resource for build tasks, input for deploy task
		if strings.Contains(task.Name, "build") {
			pipelineTask.Resources.Outputs = []tekton.PipelineTaskOutputResource{
				{
					Name:     "source",
					Resource: "source-repo",
				},
			}
		} else {
			pipelineTask.Resources.Inputs[0].From = []string{pipelineTasks[i-1].Name}
		}

		pipelineTasks = append(pipelineTasks, pipelineTask)
	}

	return pipeline.NewPipeline("skaffold-pipeline", resources, pipelineTasks), nil
}
