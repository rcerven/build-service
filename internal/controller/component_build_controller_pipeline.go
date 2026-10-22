/*
Copyright 2021-2025 Red Hat, Inc.

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

package controllers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	compv1alpha1 "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1" // TODO remove after only new model is used and old model is gone
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	oci "github.com/tektoncd/pipeline/pkg/remote/oci"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/konflux-ci/build-service/pkg/boerrors"
	"github.com/konflux-ci/build-service/pkg/common"
	gp "github.com/konflux-ci/build-service/pkg/git/gitprovider"
	l "github.com/konflux-ci/build-service/pkg/logs"
)

type BuildPipeline struct {
	Name                   string                               `json:"name,omitempty"`
	Bundle                 string                               `json:"bundle,omitempty"`
	AdditionalParams       []string                             `json:"additional-params,omitempty"`
	PipelineRefGit         *compv1alpha1.PipelineRefGit         `json:"pipelineref-by-git-resolver,omitempty"`
	PipelineSpecFromBundle *compv1alpha1.PipelineSpecFromBundle `json:"pipelinespec-from-bundle,omitempty"`
}

// BuildPipelineOld pipeline type
// TODO remove after only new model is used and old model is gone
type BuildPipelineOld struct {
	Name             string   `json:"name,omitempty"`
	Bundle           string   `json:"bundle,omitempty"`
	AdditionalParams []string `json:"additional-params,omitempty"`
}

type pipelineConfig struct {
	DefaultPipelineName string          `json:"default-pipeline-name"`
	Pipelines           []BuildPipeline `json:"pipelines"`
}

// pipelineConfigOld config type
// TODO remove after only new model is used and old model is gone
type pipelineConfigOld struct {
	DefaultPipelineName string             `json:"default-pipeline-name"`
	Pipelines           []BuildPipelineOld `json:"pipelines"`
}

// generatePaCPipelineRunConfigs generates PipelineRun YAML configs for given component.
// The generated PipelineRun Yaml content are returned in byte string and in the order of push and pull request.
func (r *ComponentBuildReconciler) generatePaCPipelineRunConfigs(ctx context.Context, component *compv1alpha1.Component, gitClient gp.GitProviderClient, versionInfo *VersionInfo, pipelineDefinition *VersionPipelineDefinition) ([]byte, []byte, error) {
	// Get pull pipeline spec
	pipelineSpecPull, err := getPipelineSpec(ctx, pipelineDefinition.Pull, component, versionInfo, gitClient, "pull")
	if err != nil {
		return nil, nil, err
	}

	// Get push pipeline spec
	pipelineSpecPush, err := getPipelineSpec(ctx, pipelineDefinition.Push, component, versionInfo, gitClient, "push")
	if err != nil {
		return nil, nil, err
	}

	pipelineRunOnPush, err := generatePaCPipelineRunForComponent(component, pipelineSpecPush, pipelineDefinition.Push, versionInfo, gitClient, false)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPushYaml, err := yaml.Marshal(pipelineRunOnPush)
	if err != nil {
		return nil, nil, err
	}

	pipelineRunOnPR, err := generatePaCPipelineRunForComponent(component, pipelineSpecPull, pipelineDefinition.Pull, versionInfo, gitClient, true)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPRYaml, err := yaml.Marshal(pipelineRunOnPR)
	if err != nil {
		return nil, nil, err
	}

	return pipelineRunOnPushYaml, pipelineRunOnPRYaml, nil
}

// getPipelineSpec retrieves pipeline spec based on the pipeline definition
// pipelineType should be "pull" or "push" for logging purposes
func getPipelineSpec(ctx context.Context, pipelineDef *PipelineDef, component *compv1alpha1.Component, versionInfo *VersionInfo, gitClient gp.GitProviderClient, pipelineType string) (*tektonapi.PipelineSpec, error) {
	log := ctrllog.FromContext(ctx)
	var pipelineSpec *tektonapi.PipelineSpec
	var err error

	if pipelineDef.PipelineSpecFromBundle != nil {
		bundleUri := pipelineDef.PipelineSpecFromBundle.Bundle
		pipelineName := pipelineDef.PipelineSpecFromBundle.Name
		if pipelineSpec, err = retrievePipelineSpec(ctx, bundleUri, pipelineName); err != nil {
			return nil, err
		}
		log.Info(fmt.Sprintf("Got %s pipeline spec from bundle for %s component, %s pipeline from %s bundle", pipelineType, component.Name, pipelineName, bundleUri), l.Audit, "true")

	} else if pipelineDef.PipelineRefGit != nil {
		if pipelineSpec, err = retrievePipelineSpecFromGit(ctx, pipelineDef.PipelineRefGit, gitClient); err != nil {
			return nil, err
		}
		log.Info(fmt.Sprintf("Got %s pipeline spec from GitRef for %s component, %s url, revision %s, pathInRepo %s",
			pipelineType, component.Name, pipelineDef.PipelineRefGit.Url, pipelineDef.PipelineRefGit.Revision, pipelineDef.PipelineRefGit.PathInRepo), l.Audit, "true")
	} else {
		repoUrl := getGitRepoUrl(*component)
		pipelineSpec = retrievePipelineSpecByName(ctx, repoUrl, versionInfo.Revision, pipelineDef.PipelineRefName, gitClient)
		if pipelineSpec != nil {
			log.Info(fmt.Sprintf("Got %s pipeline spec from Pipeline Name for %s component, pipeline name %s", pipelineType, component.Name, pipelineDef.PipelineRefName), l.Audit, "true")
		}
	}

	return pipelineSpec, nil
}

// TODO remove after only new model is used and old model is gone
// generatePaCPipelineRunConfigsOldModel generates PipelineRun YAML configs for given component.
// The generated PipelineRun Yaml content are returned in byte string and in the order of push and pull request.
func (r *ComponentBuildReconcilerOldModel) generatePaCPipelineRunConfigsOldModel(ctx context.Context, component *compapiv1alpha1.Component, gitClient gp.GitProviderClient, pacTargetBranch string) ([]byte, []byte, error) {
	log := ctrllog.FromContext(ctx)

	var pipelineName string
	var pipelineBundle string
	var pipelineRef *tektonapi.PipelineRef
	var additionalParams []string
	var err error

	// no need to check error because it would fail already in Reconcile
	pipelineRef, additionalParams, _ = r.GetBuildPipelineFromComponentAnnotation(ctx, component)
	pipelineName, pipelineBundle, err = getPipelineNameAndBundle(pipelineRef)
	if err != nil {
		return nil, nil, err
	}
	log.Info(fmt.Sprintf("Selected %s pipeline from %s bundle for %s component",
		pipelineName, pipelineBundle, component.Name),
		l.Audit, "true")

	// Get pipeline from the bundle to be expanded to the PipelineRun
	pipelineSpec, err := retrievePipelineSpec(ctx, pipelineBundle, pipelineName)
	if err != nil {
		r.EventRecorder.Eventf(component, nil, "Warning", "ErrorGettingPipelineFromBundle", "GetPipelineFromBundle", err.Error())
		return nil, nil, err
	}

	pipelineRunOnPush, err := generatePaCPipelineRunForComponentOldModel(component, pipelineSpec, additionalParams, pacTargetBranch, gitClient, false)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPushYaml, err := yaml.Marshal(pipelineRunOnPush)
	if err != nil {
		return nil, nil, err
	}

	pipelineRunOnPR, err := generatePaCPipelineRunForComponentOldModel(component, pipelineSpec, additionalParams, pacTargetBranch, gitClient, true)
	if err != nil {
		return nil, nil, err
	}
	pipelineRunOnPRYaml, err := yaml.Marshal(pipelineRunOnPR)
	if err != nil {
		return nil, nil, err
	}

	return pipelineRunOnPushYaml, pipelineRunOnPRYaml, nil
}

// retrievePipelineSpecFromGit retrieves pipeline definition from a git repository using git resolver parameters.
func retrievePipelineSpecFromGit(ctx context.Context, pipelineRefGit *compv1alpha1.PipelineRefGit, gitClient gp.GitProviderClient) (*tektonapi.PipelineSpec, error) {
	log := ctrllog.FromContext(ctx)

	// Check if the pipeline file exists
	fileExists, err := gitClient.IsFileExist(pipelineRefGit.Url, pipelineRefGit.Revision, pipelineRefGit.PathInRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to check if pipeline file exists: %w", err)
	}
	if !fileExists {
		return nil, fmt.Errorf("pipeline file not found: %s at revision %s in %s", pipelineRefGit.PathInRepo, pipelineRefGit.Revision, pipelineRefGit.Url)
	}

	log.Info("Downloading pipeline from git",
		"URL", pipelineRefGit.Url,
		"Revision", pipelineRefGit.Revision,
		"Path", pipelineRefGit.PathInRepo)

	// Download the pipeline file content
	fileContent, err := gitClient.DownloadFileContent(pipelineRefGit.Url, pipelineRefGit.Revision, pipelineRefGit.PathInRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to download pipeline from git: %w", err)
	}

	var v1Pipeline tektonapi.Pipeline
	if err := yaml.Unmarshal(fileContent, &v1Pipeline); err == nil && v1Pipeline.Kind == "Pipeline" {
		log.Info("Successfully parsed v1 pipeline from git", "Name", v1Pipeline.Name)
		return &v1Pipeline.Spec, nil
	}

	return nil, fmt.Errorf("failed to unmarshal pipeline: invalid format or unsupported version")
}

// retrievePipelineSpecByName retrieves pipeline definition by searching for it by name in the .tekton directory.
// It tries common file naming patterns.
func retrievePipelineSpecByName(ctx context.Context, gitRepoUrl, revision, pipelineName string, gitClient gp.GitProviderClient) *tektonapi.PipelineSpec {
	log := ctrllog.FromContext(ctx)

	// Common file naming patterns to try in .tekton directory
	pipelineFilePatterns := []string{
		fmt.Sprintf(".tekton/%s.yaml", pipelineName),
		fmt.Sprintf(".tekton/%s-pull-request.yaml", pipelineName),
		fmt.Sprintf(".tekton/%s-push.yaml", pipelineName),
		".tekton/pull-request.yaml",
		".tekton/push.yaml",
		".tekton/pipeline.yaml",
	}

	log.Info("Searching for pipeline by name in .tekton directory", "PipelineName", pipelineName, "URL", gitRepoUrl, "Revision", revision)

	// Try each pattern
	for _, filePath := range pipelineFilePatterns {
		// Check if file exists
		fileExists, err := gitClient.IsFileExist(gitRepoUrl, revision, filePath)
		if err != nil {
			log.Info("Error checking if file exists", "FilePath", filePath, "Error", err)
			continue
		}
		if !fileExists {
			continue
		}

		// Download the file
		fileContent, err := gitClient.DownloadFileContent(gitRepoUrl, revision, filePath)
		if err != nil {
			log.Info("Error downloading file", "FilePath", filePath, "Error", err)
			continue
		}

		var v1Pipeline tektonapi.Pipeline
		if err := yaml.Unmarshal(fileContent, &v1Pipeline); err == nil && v1Pipeline.Kind == "Pipeline" {
			// Check if the pipeline name matches
			if v1Pipeline.Name == pipelineName || v1Pipeline.GenerateName == pipelineName {
				log.Info("Found matching v1 pipeline", "Name", v1Pipeline.Name, "FilePath", filePath)
				return &v1Pipeline.Spec
			}
		}
	}

	// Pipeline not found
	return nil
}

// retrievePipelineSpec retrieves pipeline definition with given name from the given bundle.
func retrievePipelineSpec(ctx context.Context, bundleUri, pipelineName string) (*tektonapi.PipelineSpec, error) {
	var obj runtime.Object
	var err error
	resolver := oci.NewResolver(bundleUri, authn.DefaultKeychain)

	if obj, _, err = resolver.Get(ctx, "pipeline", pipelineName); err != nil {
		return nil, boerrors.NewBuildOpError(
			boerrors.EPipelineRetrievalFailed,
			fmt.Errorf("failed to retrieve pipeline %s from bundle %s: %w", pipelineName, bundleUri, err),
		)
	}

	var pipelineSpec tektonapi.PipelineSpec

	if v1Pipeline, ok := obj.(*tektonapi.Pipeline); ok {
		pipelineSpec = v1Pipeline.PipelineSpec()
	} else {
		return nil, boerrors.NewBuildOpError(
			boerrors.EPipelineRetrievalFailed,
			fmt.Errorf("failed to extract pipeline %s from bundle %s", pipelineName, bundleUri),
		)
	}

	return &pipelineSpec, nil
}

func (r *ComponentBuildReconciler) GetDefaultBuildPipelinesFromConfig(ctx context.Context) (*pipelineConfig, error) {
	pipelinesConfigMap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineConfigMapResourceName, Namespace: common.BuildServiceNamespaceName}, pipelinesConfigMap); err != nil {
		if errors.IsNotFound(err) {
			return nil, boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotDefined, err)
		}
		return nil, err
	}

	buildPipelineData := &pipelineConfig{}
	if err := yaml.Unmarshal([]byte(pipelinesConfigMap.Data[buildPipelineConfigName]), buildPipelineData); err != nil {
		return nil, boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotValid, err)
	}

	return buildPipelineData, nil
}

// GetBuildPipelineFromComponentAnnotation parses pipeline annotation on component and returns build pipeline
// TODO remove after only new model is used and old model is gone
func (r *ComponentBuildReconcilerOldModel) GetBuildPipelineFromComponentAnnotation(ctx context.Context, component *compapiv1alpha1.Component) (*tektonapi.PipelineRef, []string, error) {
	buildPipeline, err := readBuildPipelineAnnotation(component)
	if err != nil {
		return nil, nil, err
	}
	if buildPipeline == nil {
		err := fmt.Errorf("missing or empty pipeline annotation: %s, will add default one to the component", component.Annotations[defaultBuildPipelineAnnotation])
		return nil, nil, boerrors.NewBuildOpError(boerrors.EMissingPipelineAnnotation, err)
	}
	if buildPipeline.Bundle == "" || buildPipeline.Name == "" {
		err = fmt.Errorf("missing name or bundle in pipeline annotation: name=%s bundle=%s", buildPipeline.Name, buildPipeline.Bundle)
		return nil, nil, boerrors.NewBuildOpError(boerrors.EWrongPipelineAnnotation, err)
	}
	finalBundle := buildPipeline.Bundle
	additionalParams := []string{}

	pipelinesConfigMap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineConfigMapResourceName, Namespace: common.BuildServiceNamespaceName}, pipelinesConfigMap); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil, boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotDefined, err)
		}
		return nil, nil, err
	}

	buildPipelineData := &pipelineConfigOld{}
	if err := yaml.Unmarshal([]byte(pipelinesConfigMap.Data[buildPipelineConfigName]), buildPipelineData); err != nil {
		return nil, nil, boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotValid, err)
	}

	for _, pipeline := range buildPipelineData.Pipelines {
		if pipeline.Name == buildPipeline.Name {
			if buildPipeline.Bundle == "latest" {
				finalBundle = pipeline.Bundle
			}
			additionalParams = pipeline.AdditionalParams
			break
		}
	}

	// requested pipeline was not found in configMap
	if finalBundle == "latest" {
		err = fmt.Errorf("invalid pipeline name in pipeline annotation: name=%s", buildPipeline.Name)
		return nil, nil, boerrors.NewBuildOpError(boerrors.EBuildPipelineInvalid, err)
	}

	pipelineRef := &tektonapi.PipelineRef{
		ResolverRef: tektonapi.ResolverRef{
			Resolver: "bundles",
			Params: []tektonapi.Param{
				{Name: "name", Value: *tektonapi.NewStructuredValues(buildPipeline.Name)},
				{Name: "bundle", Value: *tektonapi.NewStructuredValues(finalBundle)},
				{Name: "kind", Value: *tektonapi.NewStructuredValues("pipeline")},
			},
		},
	}
	return pipelineRef, additionalParams, nil
}

// TODO remove after only new model is used and old model is gone
func readBuildPipelineAnnotation(component *compapiv1alpha1.Component) (*BuildPipelineOld, error) {
	if component.Annotations == nil {
		return nil, nil
	}

	requestedPipeline, requestedPipelineExists := component.Annotations[defaultBuildPipelineAnnotation]
	if requestedPipelineExists && requestedPipeline != "" {
		buildPipeline := &BuildPipelineOld{}
		buildPipelineBytes := []byte(requestedPipeline)

		if err := json.Unmarshal(buildPipelineBytes, buildPipeline); err != nil {
			return nil, boerrors.NewBuildOpError(boerrors.EFailedToParsePipelineAnnotation, err)
		}
		return buildPipeline, nil
	}
	return nil, nil
}

// SetDefaultBuildPipelineComponentAnnotation sets default build pipeline to component pipeline annotation
// TODO remove after only new model is used and old model is gone
func (r *ComponentBuildReconcilerOldModel) SetDefaultBuildPipelineComponentAnnotation(ctx context.Context, component *compapiv1alpha1.Component) error {
	log := ctrllog.FromContext(ctx)
	pipelinesConfigMap := &corev1.ConfigMap{}

	if err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineConfigMapResourceName, Namespace: common.BuildServiceNamespaceName}, pipelinesConfigMap); err != nil {
		if errors.IsNotFound(err) {
			return boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotDefined, err)
		}
		return err
	}

	buildPipelineData := &pipelineConfigOld{}
	if err := yaml.Unmarshal([]byte(pipelinesConfigMap.Data[buildPipelineConfigName]), buildPipelineData); err != nil {
		return boerrors.NewBuildOpError(boerrors.EBuildPipelineConfigNotValid, err)
	}

	pipelineAnnotation := fmt.Sprintf("{\"name\":\"%s\",\"bundle\":\"%s\"}", buildPipelineData.DefaultPipelineName, "latest")
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}
	component.Annotations[defaultBuildPipelineAnnotation] = pipelineAnnotation

	if err := r.Client.Update(ctx, component); err != nil {
		log.Error(err, fmt.Sprintf("failed to update component with default pipeline annotation %s", defaultBuildPipelineAnnotation))
		return err
	}
	log.Info(fmt.Sprintf("updated component with default pipeline annotation %s", defaultBuildPipelineAnnotation))
	return nil
}

// generatePaCPipelineRunForComponent returns pipeline run definition to build component source with.
// Generated pipeline run contains placeholders that are expanded by Pipeline-as-Code.
func generatePaCPipelineRunForComponent(
	component *compv1alpha1.Component,
	pipelineSpec *tektonapi.PipelineSpec,
	pipelineDefinition *PipelineDef,
	versionInfo *VersionInfo,
	gitClient gp.GitProviderClient,
	onPull bool) (*tektonapi.PipelineRun, error) {

	if versionInfo.Revision == "" {
		return nil, fmt.Errorf("target branch can't be empty for generating PaC PipelineRun for component %s, version %s", component.Name, versionInfo.SanitizedVersion)
	}
	pipelineCelExpression, err := generateCelExpressionForPipeline(component, gitClient, versionInfo, onPull)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cel expression for pipeline: %w", err)
	}
	repoUrl := getGitRepoUrl(*component)

	annotations := map[string]string{
		"pipelinesascode.tekton.dev/cancel-in-progress": "false",
		"pipelinesascode.tekton.dev/max-keep-runs":      "3",
		gitTargetBranchAnnotationName:                   "{{target_branch}}",
		pacCelExpressionAnnotationName:                  pipelineCelExpression,
		gitCommitShaAnnotationName:                      "{{revision}}",
		gitRepoAtShaAnnotationName:                      gitClient.GetBrowseRepositoryAtShaLink(repoUrl, "{{revision}}"),
		VersionAnnotationName:                           versionInfo.OriginalVersion,
	}
	labels := map[string]string{
		ComponentNameLabelName:   component.Name,
		PipelineRunTypeLabelName: "build",
	}

	imageRepo := getContainerImageRepositoryForComponent(component)
	pipelineName := getPipelineRunDefinitionName(component.Name, versionInfo.SanitizedVersion, onPull)

	var outputImageWithTag string
	if onPull {
		annotations["pipelinesascode.tekton.dev/cancel-in-progress"] = "true"
		annotations[gitPRAnnotationName] = "{{pull_request_number}}"
		outputImageWithTag = imageRepo + ":on-pr-{{revision}}"
	} else {
		outputImageWithTag = imageRepo + ":{{revision}}"
	}

	paramsInSpec := []string{}
	// pipelineSpec can be nil, when referencing pipeline only by name and it isn't found in the repository
	if pipelineSpec != nil {
		paramsInSpec = pipelineSpec.Params.GetNames()
	}

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ParamValue{Type: "string", StringVal: "{{source_url}}"}},
		{Name: "revision", Value: tektonapi.ParamValue{Type: "string", StringVal: "{{revision}}"}},
	}

	if slices.Contains(paramsInSpec, "output-image") {
		params = append(params, tektonapi.Param{Name: "output-image", Value: tektonapi.ParamValue{Type: "string", StringVal: outputImageWithTag}})
	}

	if onPull && slices.Contains(paramsInSpec, "image-expires-after") {
		prImageExpiration := os.Getenv(PipelineRunOnPRExpirationEnvVar)
		if prImageExpiration == "" {
			prImageExpiration = PipelineRunOnPRExpirationDefault
		}
		params = append(params, tektonapi.Param{Name: "image-expires-after", Value: tektonapi.ParamValue{Type: "string", StringVal: prImageExpiration}})
	}

	if pipelineSpec != nil {
		for _, additionalParam := range pipelineDefinition.AdditionalParams {
			for _, pipelineParam := range pipelineSpec.Params {
				if additionalParam == pipelineParam.Name {
					if pipelineParam.Type == "string" {
						params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "string", StringVal: pipelineParam.Default.StringVal}})
						break
					}
					if pipelineParam.Type == "array" {
						params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "array", ArrayVal: pipelineParam.Default.ArrayVal}})
						break
					}
					if pipelineParam.Type == "object" {
						params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "object", ObjectVal: pipelineParam.Default.ObjectVal}})
						break
					}
				}
			}
		}
	}

	if slices.Contains(paramsInSpec, "dockerfile") {
		params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ParamValue{Type: "string", StringVal: versionInfo.DockerfilePath}})
	}

	pathContext := getPathContext(versionInfo.Context, "")

	if slices.Contains(paramsInSpec, "path-context") {
		if pathContext != "" {
			params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ParamValue{Type: "string", StringVal: pathContext}})
			annotations[ContextAnnotationName] = pathContext
		} else {
			for _, pipelineParam := range pipelineSpec.Params {
				if pipelineParam.Name == "path-context" {
					annotations[ContextAnnotationName] = pipelineParam.Default.StringVal
					break
				}
			}
		}
	}

	var pipelineRunWorkspaces []tektonapi.WorkspaceBinding
	if pipelineSpec != nil {
		pipelineRunWorkspaces = createWorkspaceBinding(pipelineSpec.Workspaces)
	} else {
		pipelineRunWorkspaces = generateWorkspaceBinding()
	}

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        pipelineName,
			Namespace:   component.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: tektonapi.PipelineRunSpec{
			Params:     params,
			Workspaces: pipelineRunWorkspaces,
			TaskRunTemplate: tektonapi.PipelineTaskRunTemplate{
				ServiceAccountName: getBuildPipelineServiceAccountName(component.Name),
			},
		},
	}

	if pipelineDefinition.PipelineRefName != "" {
		pipelineRun.Spec.PipelineRef = &tektonapi.PipelineRef{}
		pipelineRun.Spec.PipelineRef.Name = pipelineDefinition.PipelineRefName
	} else if pipelineDefinition.PipelineSpecFromBundle != nil {
		pipelineRun.Spec.PipelineSpec = pipelineSpec
	} else if pipelineDefinition.PipelineRefGit != nil {
		pipelineRun.Spec.PipelineRef = &tektonapi.PipelineRef{}
		pipelineRun.Spec.PipelineRef.Resolver = "git"
		pipelineRun.Spec.PipelineRef.Params = tektonapi.Params{
			{Name: "url", Value: tektonapi.ParamValue{Type: "string", StringVal: pipelineDefinition.PipelineRefGit.Url}},
			{Name: "revision", Value: tektonapi.ParamValue{Type: "string", StringVal: pipelineDefinition.PipelineRefGit.Revision}},
			{Name: "pathInRepo", Value: tektonapi.ParamValue{Type: "string", StringVal: pipelineDefinition.PipelineRefGit.PathInRepo}},
		}
	}

	return pipelineRun, nil
}

// TODO remove after only new model is used and old model is gone
// generatePaCPipelineRunForComponentOldModel returns pipeline run definition to build component source with.
// Generated pipeline run contains placeholders that are expanded by Pipeline-as-Code.
func generatePaCPipelineRunForComponentOldModel(
	component *compapiv1alpha1.Component,
	pipelineSpec *tektonapi.PipelineSpec,
	additionalParams []string,
	pacTargetBranch string,
	gitClient gp.GitProviderClient,
	onPull bool) (*tektonapi.PipelineRun, error) {

	if pacTargetBranch == "" {
		return nil, fmt.Errorf("target branch can't be empty for generating PaC PipelineRun for: %v", component)
	}
	pipelineCelExpression, err := generateCelExpressionForPipelineOldModel(component, gitClient, pacTargetBranch, onPull)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cel expression for pipeline: %w", err)
	}
	repoUrl := getGitRepoUrlOldModel(*component)

	annotations := map[string]string{
		"pipelinesascode.tekton.dev/cancel-in-progress": "false",
		"pipelinesascode.tekton.dev/max-keep-runs":      "3",
		gitTargetBranchAnnotationNameOldModel:           "{{target_branch}}",
		pacCelExpressionAnnotationName:                  pipelineCelExpression,
		gitCommitShaAnnotationNameOldModel:              "{{revision}}",
		gitRepoAtShaAnnotationNameOldModel:              gitClient.GetBrowseRepositoryAtShaLink(repoUrl, "{{revision}}"),
	}
	labels := map[string]string{
		ApplicationNameLabelName:         component.Spec.Application,
		ComponentNameLabelNameOldModel:   component.Name,
		PipelineRunTypeLabelNameOldModel: "build",
	}

	imageRepo := getContainerImageRepositoryForComponentOldModel(component)
	pipelineName := getPipelineRunDefinitionNameOldModel(component.Name, onPull)

	var proposedImage string
	if onPull {
		annotations["pipelinesascode.tekton.dev/cancel-in-progress"] = "true"
		annotations[gitPRAnnotationNameOldModel] = "{{pull_request_number}}"
		proposedImage = imageRepo + ":on-pr-{{revision}}"
	} else {
		proposedImage = imageRepo + ":{{revision}}"
	}

	paramsInSpec := pipelineSpec.Params.GetNames()

	params := []tektonapi.Param{
		{Name: "git-url", Value: tektonapi.ParamValue{Type: "string", StringVal: "{{source_url}}"}},
		{Name: "revision", Value: tektonapi.ParamValue{Type: "string", StringVal: "{{revision}}"}},
	}

	if slices.Contains(paramsInSpec, "output-image") {
		params = append(params, tektonapi.Param{Name: "output-image", Value: tektonapi.ParamValue{Type: "string", StringVal: proposedImage}})
	}

	if onPull && slices.Contains(paramsInSpec, "image-expires-after") {
		prImageExpiration := os.Getenv(PipelineRunOnPRExpirationEnvVar)
		if prImageExpiration == "" {
			prImageExpiration = PipelineRunOnPRExpirationDefault
		}
		params = append(params, tektonapi.Param{Name: "image-expires-after", Value: tektonapi.ParamValue{Type: "string", StringVal: prImageExpiration}})
	}

	for _, additionalParam := range additionalParams {
		for _, pipelineParam := range pipelineSpec.Params {
			if additionalParam == pipelineParam.Name {
				if pipelineParam.Type == "string" {
					params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "string", StringVal: pipelineParam.Default.StringVal}})
					break
				}
				if pipelineParam.Type == "array" {
					params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "array", ArrayVal: pipelineParam.Default.ArrayVal}})
					break
				}
				if pipelineParam.Type == "object" {
					params = append(params, tektonapi.Param{Name: additionalParam, Value: tektonapi.ParamValue{Type: "object", ObjectVal: pipelineParam.Default.ObjectVal}})
					break
				}
			}
		}
	}

	if slices.Contains(paramsInSpec, "dockerfile") {
		if component.Spec.Source.GitSource.DockerfileURL != "" {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ParamValue{Type: "string", StringVal: component.Spec.Source.GitSource.DockerfileURL}})
		} else {
			params = append(params, tektonapi.Param{Name: "dockerfile", Value: tektonapi.ParamValue{Type: "string", StringVal: "Dockerfile"}})
		}
	}
	pathContext := getPathContext(component.Spec.Source.GitSource.Context, "")
	if pathContext != "" && slices.Contains(paramsInSpec, "path-context") {
		params = append(params, tektonapi.Param{Name: "path-context", Value: tektonapi.ParamValue{Type: "string", StringVal: pathContext}})
	}

	pipelineRunWorkspaces := createWorkspaceBinding(pipelineSpec.Workspaces)

	pipelineRun := &tektonapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PipelineRun",
			APIVersion: "tekton.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        pipelineName,
			Namespace:   component.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: tektonapi.PipelineRunSpec{
			PipelineSpec: pipelineSpec,
			Params:       params,
			Workspaces:   pipelineRunWorkspaces,
			TaskRunTemplate: tektonapi.PipelineTaskRunTemplate{
				ServiceAccountName: getBuildPipelineServiceAccountName(component.Name),
			},
		},
	}

	return pipelineRun, nil
}

func getPathContext(gitContext, dockerfileContext string) string {
	if gitContext == "" && dockerfileContext == "" {
		return ""
	}
	separator := string(filepath.Separator)
	path := filepath.Join(gitContext, dockerfileContext)
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, separator)
	return path
}

func createWorkspaceBinding(pipelineWorkspaces []tektonapi.PipelineWorkspaceDeclaration) []tektonapi.WorkspaceBinding {
	pipelineRunWorkspaces := []tektonapi.WorkspaceBinding{}
	for _, workspace := range pipelineWorkspaces {
		switch workspace.Name {
		case "workspace":
			pipelineRunWorkspaces = append(pipelineRunWorkspaces,
				tektonapi.WorkspaceBinding{
					Name:                workspace.Name,
					VolumeClaimTemplate: generateVolumeClaimTemplate(),
				})
		case "git-auth":
			pipelineRunWorkspaces = append(pipelineRunWorkspaces,
				tektonapi.WorkspaceBinding{
					Name:   workspace.Name,
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				})
		}
	}
	return pipelineRunWorkspaces
}

func generateWorkspaceBinding() []tektonapi.WorkspaceBinding {
	return []tektonapi.WorkspaceBinding{
		{
			Name:                "workspace",
			VolumeClaimTemplate: generateVolumeClaimTemplate(),
		},
		{
			Name:   "git-auth",
			Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
		},
	}
}

func generateVolumeClaimTemplate() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": resource.MustParse("1Gi"),
				},
			},
		},
	}
}

// generateCelExpressionForPipeline generates value for pipelinesascode.tekton.dev/on-cel-expression annotation
// in order to have better flexibility with git events filtering.
// Examples of returned values:
// event == "push" && target_branch == "main"
// event == "pull_request" && target_branch == "my-branch" && ( "component-src-dir/***".pathChanged() || ".tekton/pipeline.yaml".pathChanged() || "dockerfiles/my-component/Dockerfile".pathChanged() )
func generateCelExpressionForPipeline(component *compv1alpha1.Component, gitClient gp.GitProviderClient, versionInfo *VersionInfo, onPull bool) (string, error) {
	eventType := "push"
	if onPull {
		eventType = "pull_request"
	}
	eventCondition := fmt.Sprintf(`event == "%s"`, eventType)

	targetBranchCondition := fmt.Sprintf(`target_branch == "%s"`, versionInfo.Revision)
	buildEnabledCondition := fmt.Sprintf(`%s == "true"`, BuildsEnabledParamName)

	repoUrl := getGitRepoUrl(*component)

	// Set path changed event filtering only for Components that are stored within a directory of the git repository.
	pathChangedSuffix := ""
	if versionInfo.Context != "" && versionInfo.Context != "/" && versionInfo.Context != "./" && versionInfo.Context != "." {
		contextDir := versionInfo.Context
		if !strings.HasSuffix(contextDir, "/") {
			contextDir += "/"
		}

		// If a Dockerfile is defined for the Component,
		// we should rebuild the Component if the Dockerfile has been changed.
		dockerfilePathChangedSuffix := ""
		dockerfile := versionInfo.DockerfilePath
		if dockerfile != "" {
			// dockerfile could be relative to the context directory or repository root.
			// To avoid unessesary builds, it's required to pass absolute path to the Dockerfile.
			branch := versionInfo.Revision
			dockerfilePath := contextDir + dockerfile
			isDockerfileInContextDir, err := gitClient.IsFileExist(repoUrl, branch, dockerfilePath)
			if err != nil {
				return "", err
			}
			// If the Dockerfile is inside context directory, no changes to event filter needed.
			if !isDockerfileInContextDir {
				// Pipelines as Code doesn't match path if it starts from /
				dockerfileAbsolutePath := strings.TrimPrefix(dockerfile, "/")
				dockerfilePathChangedSuffix = fmt.Sprintf(`|| "%s".pathChanged() `, dockerfileAbsolutePath)
			}
		}

		pipelineFileName := getPipelineRunDefinitionFileName(component.Name, versionInfo.SanitizedVersion, onPull)

		pathChangedSuffix = fmt.Sprintf(` && ( "%s***".pathChanged() || ".tekton/%s".pathChanged() %s)`, contextDir, pipelineFileName, dockerfilePathChangedSuffix)
	}

	return fmt.Sprintf("%s && %s && %s%s", eventCondition, targetBranchCondition, buildEnabledCondition, pathChangedSuffix), nil
}

// TODO remove after only new model is used and old model is gone
// generateCelExpressionForPipelineOldModel generates value for pipelinesascode.tekton.dev/on-cel-expression annotation
// in order to have better flexibility with git events filtering.
// Examples of returned values:
// event == "push" && target_branch == "main"
// event == "pull_request" && target_branch == "my-branch" && ( "component-src-dir/***".pathChanged() || ".tekton/pipeline.yaml".pathChanged() || "dockerfiles/my-component/Dockerfile".pathChanged() )
func generateCelExpressionForPipelineOldModel(component *compapiv1alpha1.Component, gitClient gp.GitProviderClient, targetBranch string, onPull bool) (string, error) {
	eventType := "push"
	if onPull {
		eventType = "pull_request"
	}
	eventCondition := fmt.Sprintf(`event == "%s"`, eventType)

	targetBranchCondition := fmt.Sprintf(`target_branch == "%s"`, targetBranch)
	repoUrl := getGitRepoUrlOldModel(*component)

	// Set path changed event filtering only for Components that are stored within a directory of the git repository.
	pathChangedSuffix := ""
	if component.Spec.Source.GitSource.Context != "" && component.Spec.Source.GitSource.Context != "/" && component.Spec.Source.GitSource.Context != "./" && component.Spec.Source.GitSource.Context != "." {
		contextDir := component.Spec.Source.GitSource.Context
		if !strings.HasSuffix(contextDir, "/") {
			contextDir += "/"
		}

		// If a Dockerfile is defined for the Component,
		// we should rebuild the Component if the Dockerfile has been changed.
		dockerfilePathChangedSuffix := ""
		dockerfile := component.Spec.Source.GitSource.DockerfileURL
		if dockerfile != "" {
			// Ignore dockerfile that is not stored in the same git repository but downloaded by an URL.
			if !strings.Contains(dockerfile, "://") {
				// dockerfile could be relative to the context directory or repository root.
				// To avoid unessesary builds, it's required to pass absolute path to the Dockerfile.
				branch := component.Spec.Source.GitSource.Revision
				dockerfilePath := contextDir + dockerfile
				isDockerfileInContextDir, err := gitClient.IsFileExist(repoUrl, branch, dockerfilePath)
				if err != nil {
					return "", err
				}
				// If the Dockerfile is inside context directory, no changes to event filter needed.
				if !isDockerfileInContextDir {
					// Pipelines as Code doesn't match path if it starts from /
					dockerfileAbsolutePath := strings.TrimPrefix(dockerfile, "/")
					dockerfilePathChangedSuffix = fmt.Sprintf(`|| "%s".pathChanged() `, dockerfileAbsolutePath)
				}
			}
		}

		pipelineFileName := getPipelineRunDefinitionFileNameOldModel(component.Name, onPull)

		pathChangedSuffix = fmt.Sprintf(` && ( "%s***".pathChanged() || ".tekton/%s".pathChanged() %s)`, contextDir, pipelineFileName, dockerfilePathChangedSuffix)
	}

	return fmt.Sprintf("%s && %s%s", eventCondition, targetBranchCondition, pathChangedSuffix), nil
}

func getContainerImageRepositoryForComponent(component *compv1alpha1.Component) string {
	if component.Spec.ContainerImage != "" {
		return getContainerImageRepository(component.Spec.ContainerImage)
	}
	return ""
}

// TODO remove after only new model is used and old model is gone
func getContainerImageRepositoryForComponentOldModel(component *compapiv1alpha1.Component) string {
	if component.Spec.ContainerImage != "" {
		return getContainerImageRepository(component.Spec.ContainerImage)
	}
	return ""
}

// getContainerImageRepository removes tag or SHA has from container image reference
func getContainerImageRepository(image string) string {
	if strings.Contains(image, "@") {
		// registry.io/user/image@sha256:586ab...d59a
		return strings.Split(image, "@")[0]
	}
	// registry.io/user/image:tag
	return strings.Split(image, ":")[0]
}

func getPipelineNameAndBundle(pipelineRef *tektonapi.PipelineRef) (string, string, error) {
	if pipelineRef.Resolver != "" && pipelineRef.Resolver != "bundles" {
		return "", "", boerrors.NewBuildOpError(
			boerrors.EUnsupportedPipelineRef,
			fmt.Errorf("unsupported Tekton resolver %q", pipelineRef.Resolver),
		)
	}

	name := pipelineRef.Name
	var bundle string

	for _, param := range pipelineRef.Params {
		switch param.Name {
		case "name":
			name = param.Value.StringVal
		case "bundle":
			bundle = param.Value.StringVal
		}
	}

	if name == "" || bundle == "" {
		return "", "", boerrors.NewBuildOpError(
			boerrors.EMissingParamsForBundleResolver,
			fmt.Errorf("missing name or bundle in pipelineRef: name=%s bundle=%s", name, bundle),
		)
	}

	return name, bundle, nil
}

// getPipelineRunDefinitionFileName returns pipeline run definition filename of the given Component.
func getPipelineRunDefinitionFileName(componentName, versionName string, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushFilename
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRFilename
	}
	return componentName + "-" + versionName + "-" + pipelineNameSuffix
}

// TODO remove after only new model is used and old model is gone
// getPipelineRunDefinitionFileNameOldModel returns pipeline run definition filename of the given Component.
func getPipelineRunDefinitionFileNameOldModel(componentName string, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushFilename
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRFilename
	}
	return componentName + "-" + pipelineNameSuffix
}

// getPipelineRunDefinitionName generates the pipeline run definition name for the given component.
func getPipelineRunDefinitionName(componentName, versionName string, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushSuffix
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRSuffix
	}
	return componentName + "-" + versionName + pipelineNameSuffix
}

// TODO remove after only new model is used and old model is gone
// getPipelineRunDefinitionNameOldModel generates the pipeline run definition name for the given component.
func getPipelineRunDefinitionNameOldModel(componentName string, isPullRequest bool) string {
	pipelineNameSuffix := pipelineRunOnPushSuffix
	if isPullRequest {
		pipelineNameSuffix = pipelineRunOnPRSuffix
	}
	return componentName + pipelineNameSuffix
}

// TODO remove after only new model is used and old model is gone
// generateMigrationMergeRequestSourceBranch returns the source branch name used for the pipeline runs
// migration merge request of a given revision. Old components sharing the same revision are migrated
// together in a single merge request, so the branch is per-revision rather than per-component/version.
func generateMigrationMergeRequestSourceBranch(revision string) string {
	return fmt.Sprintf("%smigrate-%s", pacMergeRequestSourceBranchPrefix, revision)
}

// TODO remove after only new model is used and old model is gone
// oldModelPipelineRunCandidate is an old-model PipelineRun definition found in the .tekton directory,
// identified by the presence of the ComponentNameLabelNameOldModel label.
type oldModelPipelineRunCandidate struct {
	path             string
	pipelineRun      tektonapi.PipelineRun
	oldComponentName string
	isPullRequest    bool
}

// TODO remove after only new model is used and old model is gone
// listOldModelPipelineRunCandidates downloads and parses every file in the .tekton directory of the given
// revision, returning those that are old-model PipelineRun definitions. Used to find pipeline run files
// that were renamed by the user, so they can't be found at their expected old-model path.
func listOldModelPipelineRunCandidates(gitClient gp.GitProviderClient, repoUrl, revision string) ([]oldModelPipelineRunCandidate, error) {
	dirFiles, err := gitClient.GetFilesFromDirectory(repoUrl, revision, ".tekton")
	if err != nil {
		return nil, err
	}

	var candidates []oldModelPipelineRunCandidate
	for _, file := range dirFiles {
		content, err := gitClient.DownloadFileContent(repoUrl, revision, file.FullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to download %s: %w", file.FullPath, err)
		}
		var pr tektonapi.PipelineRun
		if err := yaml.Unmarshal(content, &pr); err != nil {
			continue
		}
		oldComponentName := pr.Labels[ComponentNameLabelNameOldModel]
		if oldComponentName == "" {
			continue
		}
		_, isPullRequest := pr.Annotations[gitPRAnnotationNameOldModel]
		candidates = append(candidates, oldModelPipelineRunCandidate{
			path:             file.FullPath,
			pipelineRun:      pr,
			oldComponentName: oldComponentName,
			isPullRequest:    isPullRequest,
		})
	}
	return candidates, nil
}

// TODO remove after only new model is used and old model is gone
// transformPipelineRunForMigration rewrites an old-model PipelineRun definition in place so it matches
// the new-model component/version naming, labels and annotations.
func transformPipelineRunForMigration(pr *tektonapi.PipelineRun, component *compv1alpha1.Component, versionInfo *VersionInfo, isPullRequest bool) {
	pr.Name = getPipelineRunDefinitionName(component.Name, versionInfo.SanitizedVersion, isPullRequest)

	if pr.Labels == nil {
		pr.Labels = map[string]string{}
	}
	delete(pr.Labels, ApplicationNameLabelName)
	delete(pr.Labels, ComponentNameLabelNameOldModel)
	pr.Labels[ComponentNameLabelName] = component.Name
	delete(pr.Labels, PipelineRunTypeLabelNameOldModel)
	pr.Labels[PipelineRunTypeLabelName] = "build"

	if pr.Annotations == nil {
		pr.Annotations = map[string]string{}
	}
	renameAnnotation(pr.Annotations, gitRepoAtShaAnnotationNameOldModel, gitRepoAtShaAnnotationName)
	renameAnnotation(pr.Annotations, gitCommitShaAnnotationNameOldModel, gitCommitShaAnnotationName)
	if isPullRequest {
		renameAnnotation(pr.Annotations, gitPRAnnotationNameOldModel, gitPRAnnotationName)
	}
	renameAnnotation(pr.Annotations, gitTargetBranchAnnotationNameOldModel, gitTargetBranchAnnotationName)

	if versionInfo.Context != "" {
		pr.Annotations[ContextAnnotationName] = versionInfo.Context
	}
	pr.Annotations[VersionAnnotationName] = versionInfo.OriginalVersion

	if celExpression, ok := pr.Annotations[pacCelExpressionAnnotationName]; ok {
		pr.Annotations[pacCelExpressionAnnotationName] = celExpression + ` && builds_enabled == "true"`
	}

	pr.Spec.TaskRunTemplate.ServiceAccountName = getBuildPipelineServiceAccountName(component.Name)
}

// TODO remove after only new model is used and old model is gone
// renameAnnotation moves the value stored under oldKey to newKey, if present.
func renameAnnotation(annotations map[string]string, oldKey, newKey string) {
	if value, ok := annotations[oldKey]; ok {
		delete(annotations, oldKey)
		annotations[newKey] = value
	}
}

// TODO remove after only new model is used and old model is gone
// collectPipelineRunMigrationFiles finds and transforms old-model pipeline run definitions for the given
// old components (all sharing the given revision), returning the combined list of repository file changes
// (renames via delete+add, or in-place content updates) needed to migrate them in a single merge request.
// Old components for which no pipeline run file could be found are returned in skippedOldComponents (not an
// error - per notes.txt this requires manual fixing and won't resolve itself on retry).
func collectPipelineRunMigrationFiles(
	ctx context.Context,
	component *compv1alpha1.Component,
	gitClient gp.GitProviderClient,
	repoUrl, revision string,
	versionInfoByOldComponent map[string]*VersionInfo,
) (files []gp.RepositoryFile, migratedOldComponents []string, skippedOldComponents []string, err error) {
	log := ctrllog.FromContext(ctx)

	candidates, err := listOldModelPipelineRunCandidates(gitClient, repoUrl, revision)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list old model pipeline run files in .tekton directory at revision %s: %w", revision, err)
	}

	oldComponentNames := slices.Collect(maps.Keys(versionInfoByOldComponent))
	slices.Sort(oldComponentNames)

	for _, oldComponentName := range oldComponentNames {
		versionInfo := versionInfoByOldComponent[oldComponentName]
		foundPush, foundPR := false, false

		for _, isPullRequest := range []bool{false, true} {
			oldPath := getPipelineRunDefinitionFilePathOldModel(oldComponentName, isPullRequest)
			exists, err := gitClient.IsFileExist(repoUrl, revision, oldPath)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to check if %s exists: %w", oldPath, err)
			}

			matchedViaRename := false
			if exists {
				content, err := gitClient.DownloadFileContent(repoUrl, revision, oldPath)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to download %s: %w", oldPath, err)
				}
				pr := &tektonapi.PipelineRun{}
				if err := yaml.Unmarshal(content, pr); err == nil && pr.Labels[ComponentNameLabelNameOldModel] == oldComponentName {
					transformPipelineRunForMigration(pr, component, versionInfo, isPullRequest)
					newContent, err := yaml.Marshal(pr)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("failed to marshal migrated pipeline run for component %s: %w", oldComponentName, err)
					}
					newPath := getPipelineRunDefinitionFilePath(component.Name, versionInfo.SanitizedVersion, isPullRequest)
					files = append(files, gp.RepositoryFile{FullPath: oldPath, Delete: true})
					files = append(files, gp.RepositoryFile{FullPath: newPath, Content: newContent})
					matchedViaRename = true
				}
			}

			if matchedViaRename {
				if isPullRequest {
					foundPR = true
				} else {
					foundPush = true
				}
				continue
			}

			for _, candidate := range candidates {
				if candidate.oldComponentName != oldComponentName || candidate.isPullRequest != isPullRequest {
					continue
				}
				pr := candidate.pipelineRun.DeepCopy()
				transformPipelineRunForMigration(pr, component, versionInfo, isPullRequest)
				newContent, err := yaml.Marshal(pr)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to marshal migrated pipeline run for component %s: %w", oldComponentName, err)
				}
				files = append(files, gp.RepositoryFile{FullPath: candidate.path, Content: newContent})
				if isPullRequest {
					foundPR = true
				} else {
					foundPush = true
				}
				break
			}
		}

		if foundPush || foundPR {
			migratedOldComponents = append(migratedOldComponents, oldComponentName)
		} else {
			log.Info("no pipeline run files found for old component, skipping its migration", "OldComponent", oldComponentName, l.Audit, "true")
			skippedOldComponents = append(skippedOldComponents, oldComponentName)
		}
	}

	return files, migratedOldComponents, skippedOldComponents, nil
}

// TODO remove after only new model is used and old model is gone
// openMigrationMergeRequest creates (or ensures) a single merge request with all given file changes
// (additions, updates and deletions) for pipeline runs migrated at the given revision.
func (r *ComponentBuildReconciler) openMigrationMergeRequest(ctx context.Context, component *compv1alpha1.Component, revision string, files []gp.RepositoryFile, gitClient gp.GitProviderClient, pacConfig map[string][]byte) (string, error) {
	log := ctrllog.FromContext(ctx)

	gitProvider, _ := getGitProvider(*component)
	repoUrl := getGitRepoUrl(*component)

	authorName := "konflux"
	authorEmail := konfluxAuthorEmail
	namePrefix := "Konflux"

	isAppUsed := common.IsPaCApplicationConfigured(gitProvider, pacConfig)
	if isAppUsed {
		if appName, appSlug, appBotUserId, err := gitClient.GetConfiguredGitAppName(); err == nil {
			namePrefix = appName
			authorName = fmt.Sprintf("%s[bot]", appSlug)
			if appBotUserId != 0 {
				authorEmail = fmt.Sprintf("%d+%s[bot]@users.noreply.github.com", appBotUserId, appSlug)
			}
		} else if gitProvider == "github" {
			log.Error(err, "failed to get PaC GitHub Application name", l.Action, l.ActionView, l.Audit, "true")
		}
	}

	commitMessage := fmt.Sprintf("%s migrate pipeline runs on %s", namePrefix, revision)
	mrData := &gp.MergeRequestData{
		CommitMessage:  commitMessage,
		SignedOff:      true,
		BranchName:     generateMigrationMergeRequestSourceBranch(revision),
		BaseBranchName: revision,
		Title:          commitMessage,
		Text:           mergeRequestDescription,
		AuthorName:     authorName,
		AuthorEmail:    authorEmail,
		Files:          files,
	}

	mrUrl, err := gitClient.EnsureMigrationMergeRequest(repoUrl, mrData)
	if err != nil {
		return "", err
	}

	var mrMessage string
	if mrUrl != "" {
		mrMessage = fmt.Sprintf("Pipeline runs migration merge request: %s, for revision: %s", mrUrl, revision)
	} else {
		mrMessage = fmt.Sprintf("Pipeline runs migration is up to date for revision: %s", revision)
	}
	log.Info(mrMessage)
	r.EventRecorder.Eventf(component, nil, "Normal", "PipelineRunsMigration", "MigratePipelineRuns", mrMessage)
	return mrUrl, nil
}

// TODO remove after only new model is used and old model is gone
// MigratePipelineRunsInRepository migrates pipeline run definitions in the Component's git repository from
// the old model to the new model, for the old Components listed in migrationMap (old component name -> new
// version name). Old components sharing the same target revision are migrated together in a single merge
// request.
//
// Returns the migration map with entries removed for old components whose migration has been resolved:
// either a merge request was successfully created/ensured for them, their target version no longer exists
// in the component spec, or no pipeline run files could be found for them (the latter two require manual
// intervention and won't resolve themselves on retry). Entries for old components that hit a transient
// error while creating their merge request are kept so they are retried on the next reconcile.
func (r *ComponentBuildReconciler) MigratePipelineRunsInRepository(ctx context.Context, component *compv1alpha1.Component, existingSpecVersions map[string]*VersionInfo, migrationMap map[string]string, gitClient gp.GitProviderClient, pacConfig map[string][]byte) (map[string]string, error) {
	log := ctrllog.FromContext(ctx).WithName("migratePipelineRuns")

	repoUrl := getGitRepoUrl(*component)

	remainingMigrationMap := make(map[string]string, len(migrationMap))
	revisionGroups := make(map[string]map[string]*VersionInfo) // revision -> old component name -> version info
	for oldComponentName, newVersionName := range migrationMap {
		remainingMigrationMap[oldComponentName] = newVersionName

		versionInfo, exists := existingSpecVersions[newVersionName]
		if !exists {
			log.Info("version referenced by migration map no longer exists in component spec, skipping migration for old component", "OldComponent", oldComponentName, "Version", newVersionName, l.Audit, "true")
			delete(remainingMigrationMap, oldComponentName)
			continue
		}

		if revisionGroups[versionInfo.Revision] == nil {
			revisionGroups[versionInfo.Revision] = make(map[string]*VersionInfo)
		}
		revisionGroups[versionInfo.Revision][oldComponentName] = versionInfo
	}

	revisions := slices.Collect(maps.Keys(revisionGroups))
	slices.Sort(revisions)

	var migrationErrors []error
	for _, revision := range revisions {
		files, migratedOldComponents, skippedOldComponents, err := collectPipelineRunMigrationFiles(ctx, component, gitClient, repoUrl, revision, revisionGroups[revision])
		if err != nil {
			log.Error(err, "failed to collect pipeline run files for migration", "Revision", revision)
			migrationErrors = append(migrationErrors, err)
			continue
		}

		for _, oldComponentName := range skippedOldComponents {
			delete(remainingMigrationMap, oldComponentName)
		}

		if len(files) == 0 {
			continue
		}

		if _, err := r.openMigrationMergeRequest(ctx, component, revision, files, gitClient, pacConfig); err != nil {
			log.Error(err, "failed to create pipeline runs migration merge request", "Revision", revision)
			migrationErrors = append(migrationErrors, err)
			continue
		}

		for _, oldComponentName := range migratedOldComponents {
			delete(remainingMigrationMap, oldComponentName)
		}
	}

	return remainingMigrationMap, stderrors.Join(migrationErrors...)
}
