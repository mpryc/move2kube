/*
 *  Copyright IBM Corporation 2021
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 */

package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/konveyor/move2kube/common"
	"github.com/konveyor/move2kube/environment"
	"github.com/konveyor/move2kube/qaengine/questionreceivers"
	environmenttypes "github.com/konveyor/move2kube/types/environment"
	transformertypes "github.com/konveyor/move2kube/types/transformer"
	"github.com/konveyor/move2kube/types/transformer/artifacts"
	"github.com/sirupsen/logrus"
)

// Executable implements transformer interface and is used to write simple external transformers
type Executable struct {
	Config     transformertypes.Transformer
	Env        *environment.Environment
	ExecConfig *ExecutableYamlConfig
}

// ExecutableYamlConfig is the format of executable yaml config
type ExecutableYamlConfig struct {
	EnableQA           bool                       `yaml:"enableQA"`
	Platforms          []string                   `yaml:"platforms"`
	DirectoryDetectCMD environmenttypes.Command   `yaml:"directoryDetectCMD"`
	TransformCMD       environmenttypes.Command   `yaml:"transformCMD"`
	Container          environmenttypes.Container `yaml:"container,omitempty"`
}

// Init Initializes the transformer
func (t *Executable) Init(tc transformertypes.Transformer, env *environment.Environment) (err error) {
	t.Config = tc
	t.ExecConfig = &ExecutableYamlConfig{}
	err = common.GetObjFromInterface(t.Config.Spec.Config, t.ExecConfig)
	if err != nil {
		logrus.Errorf("unable to load config for Transformer %+v into %T : %s", t.Config.Spec.Config, t.ExecConfig, err)
		return err
	}
	var qaRPCReceiverAddr net.Addr = nil
	if t.ExecConfig.EnableQA {
		qaRPCReceiverAddr, err = questionreceivers.StartGRPCReceiver()
		if err != nil {
			logrus.Errorf("Unable to start QA RPC Receiver engine : %s", err)
			logrus.Infof("Starting transformer that requires QA without QA.")
		}
	}
	if !common.IsPresent(t.ExecConfig.Platforms, runtime.GOOS) && t.ExecConfig.Container.Image == "" {
		return fmt.Errorf("platform %s not supported by transformer %s", runtime.GOOS, tc.Name)
	}
	t.Env, err = environment.NewEnvironment(env.EnvInfo, qaRPCReceiverAddr, t.ExecConfig.Container)
	if err != nil {
		logrus.Errorf("Unable to create Exec environment : %s", err)
		return err
	}
	return nil
}

// GetConfig returns the transformer config
func (t *Executable) GetConfig() (transformertypes.Transformer, *environment.Environment) {
	return t.Config, t.Env
}

// DirectoryDetect runs detect in each sub directory
func (t *Executable) DirectoryDetect(dir string) (services map[string][]transformertypes.Artifact, err error) {
	if t.ExecConfig.DirectoryDetectCMD == nil {
		return nil, nil
	}
	services, err = t.executeDetect(t.ExecConfig.DirectoryDetectCMD, dir)
	if err != nil {
		return services, err
	}
	for sn, ns := range services {
		for nsi, nst := range ns {
			if len(nst.Paths) == 0 {
				nst.Paths = map[transformertypes.PathType][]string{
					artifacts.ServiceDirPathType: {dir},
				}
				ns[nsi] = nst
			}
		}
		services[sn] = ns
	}
	return services, err
}

const (
	// TemplateConfigType represents the template config type
	TemplateConfigType transformertypes.ConfigType = "TemplateConfig"
)

// Transform transforms the artifacts
func (t *Executable) Transform(newArtifacts []transformertypes.Artifact, alreadySeenArtifacts []transformertypes.Artifact) (pathMappings []transformertypes.PathMapping, createdArtifacts []transformertypes.Artifact, err error) {
	pathMappings = []transformertypes.PathMapping{}
	createdArtifacts = []transformertypes.Artifact{}
	for _, a := range newArtifacts {
		if t.ExecConfig.TransformCMD == nil {
			relSrcPath, err := filepath.Rel(t.Env.GetEnvironmentSource(), a.Paths[artifacts.ServiceDirPathType][0])
			if err != nil {
				logrus.Errorf("Unable to convert source path %s to be relative : %s", a.Paths[artifacts.ServiceDirPathType][0], err)
				continue
			}
			var config interface{}
			if a.Configs != nil {
				config = a.Configs[TemplateConfigType]
			}
			pathMappings = append(pathMappings, transformertypes.PathMapping{
				Type:           transformertypes.TemplatePathMappingType,
				SrcPath:        filepath.Join(t.Env.Context, t.Env.RelTemplatesDir),
				DestPath:       filepath.Join(common.DefaultSourceDir, relSrcPath),
				TemplateConfig: config,
			}, transformertypes.PathMapping{
				Type:     transformertypes.SourcePathMappingType,
				SrcPath:  "",
				DestPath: common.DefaultSourceDir,
			})
		} else {
			path := ""
			if a.Paths != nil && a.Paths[artifacts.ServiceDirPathType] != nil {
				path = a.Paths[artifacts.ServiceDirPathType][0]
			}
			stdout, stderr, exitcode, err := t.Env.Exec(append(t.ExecConfig.TransformCMD, path))
			if err != nil {
				if errors.Is(err, &environment.EnvironmentNotActiveError{}) {
					logrus.Debugf("%s", err)
					continue
				}
				logrus.Errorf("Transform failed %s : %s : %d : %s", stdout, stderr, exitcode, err)
				continue
			} else if exitcode != 0 {
				logrus.Debugf("Transform did not succeed %s : %s : %d : %s", stdout, stderr, exitcode, err)
				continue
			}
			logrus.Debugf("%s Transform succeeded in %s : %s, %s, %d", t.Config.Name, t.Env.Decode(path), stdout, stderr, exitcode)
			stdout = strings.TrimSpace(stdout)
			var output transformertypes.TransformOutput
			err = json.Unmarshal([]byte(stdout), &output)
			if err != nil {
				logrus.Errorf("Error in unmarshalling json %s: %s.", stdout, err)
			}
			pathMappings = append(pathMappings, output.PathMappings...)
			createdArtifacts = append(createdArtifacts, output.CreatedArtifacts...)
		}
	}
	return pathMappings, createdArtifacts, nil
}

func (t *Executable) executeDetect(cmd environmenttypes.Command, dir string) (services map[string][]transformertypes.Artifact, err error) {
	stdout, stderr, exitcode, err := t.Env.Exec(append(cmd, dir))
	if err != nil {
		if errors.Is(err, &environment.EnvironmentNotActiveError{}) {
			logrus.Debugf("%s", err)
			return nil, err
		}
		logrus.Errorf("Detect failed %s : %s : %d : %s", stdout, stderr, exitcode, err)
		return nil, err
	} else if exitcode != 0 {
		logrus.Debugf("Detect did not succeed %s : %s : %d", stdout, stderr, exitcode)
		return nil, nil
	}
	logrus.Debugf("%s Detect succeeded in %s : %s, %s, %d", t.Config.Name, t.Env.Decode(dir), stdout, stderr, exitcode)
	stdout = strings.TrimSpace(stdout)
	var output map[string][]transformertypes.Artifact
	err = json.Unmarshal([]byte(stdout), &output)
	if err != nil {
		logrus.Debugf("Error in unmarshalling output json to full detect output %s: %s.", stdout, err)
	} else {
		return output, nil
	}
	var config map[string]interface{}
	if stdout != "" {
		config = map[string]interface{}{}
		err = json.Unmarshal([]byte(stdout), &config)
		if err != nil {
			logrus.Debugf("Error in unmarshalling json %s: %s.", stdout, err)
		}
	}
	trans := transformertypes.Artifact{
		Paths: map[transformertypes.PathType][]string{artifacts.ServiceDirPathType: {dir}},
		Configs: map[transformertypes.ConfigType]interface{}{
			TemplateConfigType: config,
		},
	}
	return map[string][]transformertypes.Artifact{"": {trans}}, nil
}
