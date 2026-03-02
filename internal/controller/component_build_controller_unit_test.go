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
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/konflux-ci/build-service/pkg/bometrics"
	. "github.com/konflux-ci/build-service/pkg/common"
	pkgslices "github.com/konflux-ci/build-service/pkg/slices"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	ghAppPrivateKeyStub    = "-----BEGIN RSA PRIVATE KEY-----_key-content_-----END RSA PRIVATE KEY-----"
	testPipelineAnnotation = "{\"name\":\"pipeline_name\",\"bundle\":\"bundle_name\"}"
)

func TestGetProvisionTimeMetricsBuckets(t *testing.T) {
	buckets := bometrics.HistogramBuckets
	for i := 1; i < len(buckets); i++ {
		if buckets[i] <= buckets[i-1] {
			t.Errorf("Buckets must be in increasing order, but got: %v", buckets)
		}
	}
}

// TODO remove after only new model is used
func TestReadBuildStatusOldModel(t *testing.T) {
	tests := []struct {
		name                       string
		buildStatusAnnotationValue string
		want                       *BuildStatus
	}{
		{
			name:                       "should be able to read build status with all fields",
			buildStatusAnnotationValue: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
			want: &BuildStatus{
				PaC: &PaCBuildStatus{
					State:             "enabled",
					ConfigurationTime: "time",
					ErrorInfo: ErrorInfo{
						ErrId:      5,
						ErrMessage: "pac-error",
					},
				},
				Message: "done",
			},
		},
		{
			name:                       "should return empty build status if annotation is empty or not set",
			buildStatusAnnotationValue: "",
			want:                       &BuildStatus{},
		},
		{
			name:                       "should return empty build status if curent one is not valid JSON",
			buildStatusAnnotationValue: "{\"pac\":{\"build-start-time\":\"time\"}",
			want:                       &BuildStatus{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
					Annotations: map[string]string{
						BuildStatusAnnotationName: tt.buildStatusAnnotationValue,
					},
				},
			}
			got := readBuildStatus(component)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("readBuildStatus(): actual: %v, want %v", got, tt.want)
			}
		})
	}
}

// TODO remove after only new model is used
func TestWriteBuildStatusOldModel(t *testing.T) {
	tests := []struct {
		name        string
		component   *compapiv1alpha1.Component
		buildStatus *BuildStatus
		want        string
	}{
		{
			name: "should be able to write build status with all fields",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-component",
					Namespace:   "my-namespace",
					Annotations: map[string]string{},
				},
			},
			buildStatus: &BuildStatus{
				PaC: &PaCBuildStatus{
					State: "enabled",
					ErrorInfo: ErrorInfo{
						ErrId:      5,
						ErrMessage: "pac-error",
					},
					ConfigurationTime: "time",
				},
				Message: "done",
			},
			want: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to write build status when annotations is nil",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
				},
			},
			buildStatus: &BuildStatus{
				PaC: &PaCBuildStatus{
					State: "enabled",
					ErrorInfo: ErrorInfo{
						ErrId:      5,
						ErrMessage: "pac-error",
					},
					ConfigurationTime: "time",
				},
				Message: "done",
			},
			want: "{\"pac\":{\"state\":\"enabled\",\"configuration-time\":\"time\",\"error-id\":5,\"error-message\":\"pac-error\"},\"message\":\"done\"}",
		},
		{
			name: "should be able to overwrite build status",
			component: &compapiv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-component",
					Namespace: "my-namespace",
					Annotations: map[string]string{
						BuildStatusAnnotationName: "{\"pac\":{\"state\":\"error\"},\"message\":\"done\"}",
					},
				},
			},
			buildStatus: &BuildStatus{
				PaC: &PaCBuildStatus{
					State: "error",
					ErrorInfo: ErrorInfo{
						ErrId:      10,
						ErrMessage: "error-ion-pac",
					},
					ConfigurationTime: "time",
				},
				Message: "done",
			},
			want: "{\"pac\":{\"state\":\"error\",\"configuration-time\":\"time\",\"error-id\":10,\"error-message\":\"error-ion-pac\"},\"message\":\"done\"}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeBuildStatus(tt.component, tt.buildStatus)
			got := tt.component.Annotations[BuildStatusAnnotationName]
			if got != tt.want {
				t.Errorf("writeBuildStatus(): actual: %v, want %v", got, tt.want)
			}
		})
	}
}

// TODO remove after only new model is used
func TestGeneratePaCPipelineRunForComponentOldModel(t *testing.T) {
	component := &compapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-component",
			Namespace: "my-namespace",
			Annotations: map[string]string{
				"skip-initial-checks":          "true",
				GitProviderAnnotationName:      "github",
				defaultBuildPipelineAnnotation: testPipelineAnnotation,
			},
		},
		Spec: compapiv1alpha1.ComponentSpec{
			Application:    "my-application",
			ContainerImage: "registry.io/username/image:tag",
			Source: compapiv1alpha1.ComponentSource{
				ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
					GitSource: &compapiv1alpha1.GitSource{
						URL:           "https://githost.com/user/repo.git",
						Context:       "./base_context",
						DockerfileURL: "containerFile",
					},
				},
			},
		},
		Status: compapiv1alpha1.ComponentStatus{},
	}
	param1_value := "param1_value"
	param2_value := []string{"param2_value1", "param2_value2"}
	pipelineSpec := &tektonapi.PipelineSpec{
		Workspaces: []tektonapi.PipelineWorkspaceDeclaration{
			{
				Name: "git-auth",
			},
			{
				Name: "workspace",
			},
		},
		Params: tektonapi.ParamSpecs{
			{Name: "add-param1", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: param1_value}},
			{Name: "add-param2", Type: "array", Default: &tektonapi.ParamValue{Type: "array", ArrayVal: param2_value}},
			{Name: "output-image", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "image-expires-after", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "dockerfile", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "path-context", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
		},
	}
	branchName := "custom-branch"
	ResetTestGitProviderClient()

	pipelineRun, err := generatePaCPipelineRunForComponentOldModel(component, pipelineSpec, []string{"add-param1", "add-param2", "non-existing"}, branchName, testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run")
	}

	if pipelineRun.Name != component.Name+pipelineRunOnPRSuffix {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipeline name")
	}
	if pipelineRun.Namespace != "my-namespace" {
		t.Error("generatePaCPipelineRunForComponent(): pipeline namespace doesn't match")
	}

	if pipelineRun.Labels[ApplicationNameLabelName] != "my-application" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ApplicationNameLabelName)
	}
	if pipelineRun.Labels[ComponentNameLabelName] != "my-component" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ComponentNameLabelName)
	}
	if pipelineRun.Labels["pipelines.appstudio.openshift.io/type"] != "build" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelines.appstudio.openshift.io/type label value")
	}

	onCel := `event == "pull_request" && target_branch == "custom-branch" && ( "./base_context/***".pathChanged() || ".tekton/my-component-pull-request.yaml".pathChanged() )`
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/on-cel-expression"] != onCel {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/on-cel-expression annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/max-keep-runs"] != "3" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/max-keep-runs annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/cancel-in-progress"] != "true" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/cancel-in-progress annotation value")
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/target_branch"] != "{{target_branch}}" {
		t.Error("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/target_branch annotation value")
	}
	if pipelineRun.Annotations[gitCommitShaAnnotationName] != "{{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitCommitShaAnnotationName)
	}
	if pipelineRun.Annotations[gitRepoAtShaAnnotationName] != "https://githost.com/user/repo?rev={{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitRepoAtShaAnnotationName)
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/pull_request_number"] != "{{pull_request_number}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/pull_request_number annotation value")
	}

	if len(pipelineRun.Spec.Params) != 8 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline params")
	}
	for _, param := range pipelineRun.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "{{source_url}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "{{revision}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:on-pr-{{revision}}") {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "image-expires-after":
			if param.Value.StringVal != "5d" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "dockerfile":
			if param.Value.StringVal != "containerFile" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "path-context":
			if param.Value.StringVal != "base_context" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param1":
			if param.Value.StringVal != param1_value {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param2":
			if len(param.Value.ArrayVal) != len(param2_value) {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
			for idx := range param2_value {
				if param2_value[idx] != param.Value.ArrayVal[idx] {
					t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)

				}
			}
		default:
			t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}

	if len(pipelineRun.Spec.Workspaces) != 2 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline workspaces")
	}
	for _, workspace := range pipelineRun.Spec.Workspaces {
		if workspace.Name == "workspace" {
			continue
		}
		if workspace.Name == "git-auth" {
			continue
		}
		t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline workspaces %v", workspace)
	}

	if pipelineRun.Spec.TaskRunTemplate.ServiceAccountName != "build-pipeline-"+component.Name {
		t.Error("generatePaCPipelineRunForComponent(): build pipeline service account is incorrect")
	}
}

func TestGeneratePaCPipelineRunForComponent(t *testing.T) {
	component := getComponentData(componentConfig{
		componentKey: types.NamespacedName{Name: "my-component", Namespace: "my-namespace"},
		versions: []compapiv1alpha1.ComponentVersion{
			{Name: "version1", Revision: "custom-branch", Context: "./base_context", DockerfileURI: "containerFile"},
		},
	})
	existingSpecVersions := buildVersionInfoMap(component, false)
	param1_value := "param1_value"
	param2_value := []string{"param2_value1", "param2_value2"}
	pipelineSpec := &tektonapi.PipelineSpec{
		Workspaces: []tektonapi.PipelineWorkspaceDeclaration{
			{
				Name: "git-auth",
			},
			{
				Name: "workspace",
			},
		},
		Params: tektonapi.ParamSpecs{
			{Name: "add-param1", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: param1_value}},
			{Name: "add-param2", Type: "array", Default: &tektonapi.ParamValue{Type: "array", ArrayVal: param2_value}},
			{Name: "output-image", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "image-expires-after", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "dockerfile", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "path-context", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
		},
	}
	pipelineDefinition := &PipelineDef{AdditionalParams: []string{"add-param1", "add-param2", "non-existing"}}
	ResetTestGitProviderClient()

	pipelineRun, err := generatePaCPipelineRunForComponent(component, pipelineSpec, pipelineDefinition, existingSpecVersions["version1"], testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run")
	}

	if pipelineRun.Name != component.Name+"-"+existingSpecVersions["version1"].SanitizedVersion+pipelineRunOnPRSuffix {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipeline name")
	}
	if pipelineRun.Namespace != "my-namespace" {
		t.Error("generatePaCPipelineRunForComponent(): pipeline namespace doesn't match")
	}

	if pipelineRun.Labels[ComponentNameLabelName] != "my-component" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s label value", ComponentNameLabelName)
	}
	if pipelineRun.Labels["pipelines.appstudio.openshift.io/type"] != "build" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelines.appstudio.openshift.io/type label value")
	}

	onCel := `event == "pull_request" && target_branch == "custom-branch" && ( "./base_context/***".pathChanged() || ".tekton/my-component-version1-pull-request.yaml".pathChanged() )`
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/on-cel-expression"] != onCel {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/on-cel-expression annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/max-keep-runs"] != "3" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/max-keep-runs annotation value")
	}
	if pipelineRun.Annotations["pipelinesascode.tekton.dev/cancel-in-progress"] != "true" {
		t.Error("generatePaCPipelineRunForComponent(): wrong pipelinesascode.tekton.dev/cancel-in-progress annotation value")
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/target_branch"] != "{{target_branch}}" {
		t.Error("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/target_branch annotation value")
	}
	if pipelineRun.Annotations[gitCommitShaAnnotationName] != "{{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitCommitShaAnnotationName)
	}
	if pipelineRun.Annotations[gitRepoAtShaAnnotationName] != "https://githost.com/user/repo?rev={{revision}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", gitRepoAtShaAnnotationName)
	}
	if pipelineRun.Annotations["build.appstudio.redhat.com/pull_request_number"] != "{{pull_request_number}}" {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong build.appstudio.redhat.com/pull_request_number annotation value")
	}
	if pipelineRun.Annotations[VersionAnnotationName] != existingSpecVersions["version1"].OriginalVersion {
		t.Errorf("generatePaCPipelineRunForComponent(): wrong %s annotation value", VersionAnnotationName)
	}

	if len(pipelineRun.Spec.Params) != 8 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline params")
	}
	for _, param := range pipelineRun.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "{{source_url}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "{{revision}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:on-pr-{{revision}}") {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "image-expires-after":
			if param.Value.StringVal != "5d" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "dockerfile":
			if param.Value.StringVal != "containerFile" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "path-context":
			if param.Value.StringVal != "base_context" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param1":
			if param.Value.StringVal != param1_value {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param2":
			if len(param.Value.ArrayVal) != len(param2_value) {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
			for idx := range param2_value {
				if param2_value[idx] != param.Value.ArrayVal[idx] {
					t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)

				}
			}
		default:
			t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}

	if len(pipelineRun.Spec.Workspaces) != 2 {
		t.Error("generatePaCPipelineRunForComponent(): wrong number of pipeline workspaces")
	}
	for _, workspace := range pipelineRun.Spec.Workspaces {
		if workspace.Name == "workspace" {
			continue
		}
		if workspace.Name == "git-auth" {
			continue
		}
		t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline workspaces %v", workspace)
	}

	if pipelineRun.Spec.TaskRunTemplate.ServiceAccountName != "build-pipeline-"+component.Name {
		t.Error("generatePaCPipelineRunForComponent(): build pipeline service account is incorrect")
	}

	// test that if default params aren't in the spec, we won't add them
	pipelineSpec2 := &tektonapi.PipelineSpec{
		Params: tektonapi.ParamSpecs{
			{Name: "add-param1", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: param1_value}},
			{Name: "add-param2", Type: "array", Default: &tektonapi.ParamValue{Type: "array", ArrayVal: param2_value}},
			{Name: "output-image", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
			{Name: "image-expires-after", Type: "string", Default: &tektonapi.ParamValue{Type: "string", StringVal: ""}},
		},
	}
	pipelineRun2, err := generatePaCPipelineRunForComponent(component, pipelineSpec2, pipelineDefinition, existingSpecVersions["version1"], testGitProviderClient, true)
	if err != nil {
		t.Error("generatePaCPipelineRunForComponent(): Failed to generate pipeline run 2")
	}

	for _, param := range pipelineRun2.Spec.Params {
		switch param.Name {
		case "git-url":
			if param.Value.StringVal != "{{source_url}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s", param.Name)
			}
		case "revision":
			if param.Value.StringVal != "{{revision}}" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "output-image":
			if !strings.HasPrefix(param.Value.StringVal, "registry.io/username/image:on-pr-{{revision}}") {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "image-expires-after":
			if param.Value.StringVal != "5d" {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param1":
			if param.Value.StringVal != param1_value {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
		case "add-param2":
			if len(param.Value.ArrayVal) != len(param2_value) {
				t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)
			}
			for idx := range param2_value {
				if param2_value[idx] != param.Value.ArrayVal[idx] {
					t.Errorf("generatePaCPipelineRunForComponent(): wrong pipeline parameter %s value", param.Name)

				}
			}
		default:
			t.Errorf("generatePaCPipelineRunForComponent(): unexpected pipeline parameter %v", param)
		}
	}
}

// TODO remove after only new model is used
func TestGeneratePaCPipelineRunForComponent_ShouldStopIfTargetBranchIsNotSetOldModel(t *testing.T) {
	_, err := generatePaCPipelineRunForComponentOldModel(nil, nil, nil, "", nil, true)
	if err == nil {
		t.Errorf("generatePaCPipelineRunForComponent(): expected error")
	}
}

func TestGeneratePaCPipelineRunForComponent_ShouldStopIfTargetBranchIsNotSet(t *testing.T) {
	_, err := generatePaCPipelineRunForComponent(&compapiv1alpha1.Component{ObjectMeta: metav1.ObjectMeta{Name: "my-component", Namespace: "my-namespace"}},
		nil, nil, &VersionInfo{}, nil, true)
	if err == nil {
		t.Errorf("generatePaCPipelineRunForComponent(): expected error")
	}
}

// TODO remove after only new model is used
func TestGenerateCelExpressionForPipelineOldModel(t *testing.T) {
	componentKey := types.NamespacedName{Namespace: "test-ns", Name: "component-name"}
	ResetTestGitProviderClient()

	tests := []struct {
		name              string
		component         *compapiv1alpha1.Component
		targetBranch      string
		isDockerfileExist func(repoUrl, branch, dockerfilePath string) (bool, error)
		wantOnPullError   bool
		wantOnPushError   bool
		wantOnPull        string
		wantOnPush        string
	}{
		{
			name: "should generate cel expression for component that occupies whole git repository",
			component: func() *compapiv1alpha1.Component {
				component := getSampleComponentDataOldModel(componentKey)
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch"`,
			wantOnPush:   `event == "push" && target_branch == "my-branch"`,
		},
		{
			name: "should generate cel expression for component with context directory, without dokerfile",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile in context directory",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "dockerfile/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return true, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside context directory",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "docker-root-dir/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, nil
			},
			wantOnPull: `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			wantOnPush: `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside git repository",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "https://host.com:1234/files/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			wantOnPull:   `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-pull-request.yaml".pathChanged() )`,
			wantOnPush:   `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/component-name-push.yaml".pathChanged() )`,
		},
		{
			name: "should fail to generate cel expression for component if isFileExist fails",
			component: func() *compapiv1alpha1.Component {
				component := getComponentDataOldModel(componentConfigOldModel{componentKey: componentKey, gitSourceContext: "component-dir"})
				component.Spec.Source.GitSource.DockerfileURL = "non-existing/Dockerfile"
				return component
			}(),
			targetBranch: "my-branch",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, fmt.Errorf("Failed to check file existance")
			},
			wantOnPullError: true,
			wantOnPushError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isDockerfileExist != nil {
				IsFileExistFunc = tt.isDockerfileExist
			} else {
				IsFileExistFunc = func(repoUrl, branchName, filePath string) (bool, error) {
					t.Errorf("IsFileExist should not be invoked")
					return false, nil
				}
			}

			got, err := generateCelExpressionForPipelineOldModel(tt.component, testGitProviderClient, tt.targetBranch, true)
			if err != nil {
				if !tt.wantOnPullError {
					t.Errorf("generateCelExpressionForPipeline(on pull): got err: %v", err)
				}
			} else {
				if got != tt.wantOnPull {
					t.Errorf("generateCelExpressionForPipeline(on pull): got '%s', want '%s'", got, tt.wantOnPull)
				}
			}

			got, err = generateCelExpressionForPipelineOldModel(tt.component, testGitProviderClient, tt.targetBranch, false)
			if err != nil {
				if !tt.wantOnPushError {
					t.Errorf("generateCelExpressionForPipeline(on push): got err: %v", err)
				}
			}
			if got != tt.wantOnPush {
				t.Errorf("generateCelExpressionForPipeline(on push): got '%s', want '%s'", got, tt.wantOnPush)
			}
		})
	}
	ResetTestGitProviderClient()
}

func TestGenerateCelExpressionForPipeline(t *testing.T) {
	componentKey := types.NamespacedName{Namespace: "test-ns", Name: "comp1"}
	ResetTestGitProviderClient()

	tests := []struct {
		name              string
		isDockerfileExist func(repoUrl, branch, dockerfilePath string) (bool, error)
		wantOnPullError   bool
		wantOnPushError   bool
		wantOnPull        string
		wantOnPush        string
		versionInfo       *VersionInfo
	}{
		{
			name:        "should generate cel expression for component that occupies whole git repository",
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch"`,
			wantOnPush:  `event == "push" && target_branch == "my-branch"`,
			versionInfo: &VersionInfo{Revision: "my-branch", SanitizedVersion: "version1"},
		},
		{
			name:        "should generate cel expression for component with context directory, without dokerfile",
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", SanitizedVersion: "version1"},
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile in context directory",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return true, nil
			},
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "dockerfile/Dockerfile", SanitizedVersion: "version1"},
		},
		{
			name: "should generate cel expression for component with context directory and its dockerfile outside context directory",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, nil
			},
			wantOnPull:  `event == "pull_request" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-pull-request.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			wantOnPush:  `event == "push" && target_branch == "my-branch" && ( "component-dir/***".pathChanged() || ".tekton/comp1-version1-push.yaml".pathChanged() || "docker-root-dir/Dockerfile".pathChanged() )`,
			versionInfo: &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "docker-root-dir/Dockerfile", SanitizedVersion: "version1"},
		},
		{
			name: "should fail to generate cel expression for component if isFileExist fails",
			isDockerfileExist: func(repoUrl, branch, dockerfilePath string) (bool, error) {
				return false, fmt.Errorf("Failed to check file existance")
			},
			wantOnPullError: true,
			wantOnPushError: true,
			versionInfo:     &VersionInfo{Revision: "my-branch", Context: "component-dir", DockerfileURI: "non-existing/Dockerfile", SanitizedVersion: "version1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isDockerfileExist != nil {
				IsFileExistFunc = tt.isDockerfileExist
			} else {
				IsFileExistFunc = func(repoUrl, branchName, filePath string) (bool, error) {
					t.Errorf("IsFileExist should not be invoked")
					return false, nil
				}
			}
			component := getSampleComponentData(componentKey)
			got, err := generateCelExpressionForPipeline(component, testGitProviderClient, tt.versionInfo, true)
			if err != nil {
				if !tt.wantOnPullError {
					t.Errorf("generateCelExpressionForPipeline(on pull): got err: %v", err)
				}
			} else {
				if got != tt.wantOnPull {
					t.Errorf("generateCelExpressionForPipeline(on pull): got '%s', want '%s'", got, tt.wantOnPull)
				}
			}

			got, err = generateCelExpressionForPipeline(component, testGitProviderClient, tt.versionInfo, false)
			if err != nil {
				if !tt.wantOnPushError {
					t.Errorf("generateCelExpressionForPipeline(on push): got err: %v", err)
				}
			}
			if got != tt.wantOnPush {
				t.Errorf("generateCelExpressionForPipeline(on push): got '%s', want '%s'", got, tt.wantOnPush)
			}
		})
	}
	ResetTestGitProviderClient()
}

func TestGetContainerImageRepository(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "should not change image",
			image: "image-name",
			want:  "image-name",
		},
		{
			name:  "should not change /user/image",
			image: "user/image",
			want:  "user/image",
		},
		{
			name:  "should not change repository.io/user/image",
			image: "repository.io/user/image",
			want:  "repository.io/user/image",
		},
		{
			name:  "should delete tag",
			image: "repository.io/user/image:tag",
			want:  "repository.io/user/image",
		},
		{
			name:  "should delete sha",
			image: "repository.io/user/image@sha256:586ab46b9d6d906b2df3dad12751e807bd0f0632d5a2ab3991bdac78bdccd59a",
			want:  "repository.io/user/image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getContainerImageRepository(tt.image)
			if got != tt.want {
				t.Errorf("getContainerImageRepository(): got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePaCConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		gitProvider string
		secret      corev1.Secret
		expectError bool
	}{
		{
			name:        "should accept GitHub application configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub application configuration with end line",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub + "\n"),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub token configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("ghp_token"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitHub basic auth configuration",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("password"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitHub access token",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(""),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject empty GitHub password",
			gitProvider: "github",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
				},
			},
			expectError: true,
		},
		{
			name:        "should accept GitHub application configuration if both GitHub application and webhook configured",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
					"password":                      []byte("ghp_token"),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject GitHub application configuration if GitHub id is missing",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
					"password":                      []byte("ghp_token"),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application private key is missing",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey: []byte("12345"),
					"password":                    []byte("ghp_token"),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application id is invalid",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12ab"),
					PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject GitHub application configuration if GitHub application application private key is invalid",
			gitProvider: "github",
			secret: corev1.Secret{
				Data: map[string][]byte{
					PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
					PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				},
			},
			expectError: true,
		},
		{
			name:        "should accept GitLab webhook configuration",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("token"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should accept GitLab basic auth configuration",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte(base64.StdEncoding.EncodeToString([]byte("user"))),
					"password": []byte(base64.StdEncoding.EncodeToString([]byte("password"))),
				},
			},
			expectError: false,
		},
		{
			name:        "should reject empty GitLab webhook token",
			gitProvider: "gitlab",
			secret: corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"password": []byte(""),
				},
			},
			expectError: true,
		},
		{
			name:        "should reject unknown application configuration",
			gitProvider: "unknown",
			secret: corev1.Secret{
				Data: map[string][]byte{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePaCConfiguration(tt.gitProvider, tt.secret)
			if err != nil {
				if !tt.expectError {
					t.Errorf("Expected that the configuration %#v from provider %s should be valid", tt.secret.Data, tt.gitProvider)
				}
			} else {
				if tt.expectError {
					t.Errorf("Expected that the configuration %#v from provider %s fails", tt.secret.Data, tt.gitProvider)
				}
			}
		})
	}
}

func TestGeneratePaCWebhookSecretString(t *testing.T) {
	expectedSecretStringLength := 20 * 2 // each byte is represented by 2 hex chars

	t.Run("should be able to generate webhook secret string", func(t *testing.T) {
		secret := generatePaCWebhookSecretString()
		if len(secret) != expectedSecretStringLength {
			t.Errorf("Expected that webhook secret string has length %d, but got %d", expectedSecretStringLength, len(secret))
		}
	})

	t.Run("should generate different webhook secret strings", func(t *testing.T) {
		n := 100
		secrets := make([]string, n)
		for i := 0; i < n; i++ {
			secrets[i] = generatePaCWebhookSecretString()
		}

		secret := secrets[0]
		for i := 1; i < n; i++ {
			if secret == secrets[i] {
				t.Errorf("All webhook secrets strings must be different")
			}
		}
	})
}

func TestGetPathContext(t *testing.T) {
	tests := []struct {
		name              string
		gitContext        string
		dockerfileContext string
		want              string
	}{
		{
			name:              "should return empty context if both contexts empty",
			gitContext:        "",
			dockerfileContext: "",
			want:              "",
		},
		{
			name:              "should use current directory from git context",
			gitContext:        ".",
			dockerfileContext: "",
			want:              ".",
		},
		{
			name:              "should use current directory from dockerfile context",
			gitContext:        "",
			dockerfileContext: ".",
			want:              ".",
		},
		{
			name:              "should use current directory if both contexts are current directory",
			gitContext:        ".",
			dockerfileContext: ".",
			want:              ".",
		},
		{
			name:              "should use git context if dockerfile context if not set",
			gitContext:        "dir",
			dockerfileContext: "",
			want:              "dir",
		},
		{
			name:              "should use dockerfile context if git context if not set",
			gitContext:        "",
			dockerfileContext: "dir",
			want:              "dir",
		},
		{
			name:              "should use git context if dockerfile context is current directory",
			gitContext:        "dir",
			dockerfileContext: ".",
			want:              "dir",
		},
		{
			name:              "should use dockerfile context if git context is current directory",
			gitContext:        ".",
			dockerfileContext: "dir",
			want:              "dir",
		},
		{
			name:              "should respect both git and dockerfile contexts",
			gitContext:        "component-dir",
			dockerfileContext: "dockerfile-dir",
			want:              "component-dir/dockerfile-dir",
		},
		{
			name:              "should respect both git and dockerfile contexts in subfolders",
			gitContext:        "path/to/component",
			dockerfileContext: "path/to/dockercontext/",
			want:              "path/to/component/path/to/dockercontext",
		},
		{
			name:              "should remove slash at the end",
			gitContext:        "path/to/dir/",
			dockerfileContext: "",
			want:              "path/to/dir",
		},
		{
			name:              "should remove slash at the end",
			gitContext:        "",
			dockerfileContext: "path/to/dir/",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "/path/to/dir/",
			dockerfileContext: "",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "",
			dockerfileContext: "/path/to/dir/",
			want:              "path/to/dir",
		},
		{
			name:              "should not allow absolute path",
			gitContext:        "/path/to/dir1/",
			dockerfileContext: "/path/to/dir2/",
			want:              "path/to/dir1/path/to/dir2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPathContext(tt.gitContext, tt.dockerfileContext)
			if got != tt.want {
				t.Errorf("Expected \"%s\", but got \"%s\"", tt.want, got)
			}
		})
	}
}

func TestCreateWorkspaceBinding(t *testing.T) {
	tests := []struct {
		name                      string
		pipelineWorkspaces        []tektonapi.PipelineWorkspaceDeclaration
		expectedWorkspaceBindings []tektonapi.WorkspaceBinding
	}{
		{
			name: "should not bind unknown workspaces",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "unknown1",
				},
				{
					Name: "unknown2",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{},
		},
		{
			name: "should bind git-auth",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "git-auth",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{
				{
					Name:   "git-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				},
			},
		},
		{
			name: "should bind git-auth and workspace, should not bind unknown",
			pipelineWorkspaces: []tektonapi.PipelineWorkspaceDeclaration{
				{
					Name: "git-auth",
				},
				{
					Name: "unknown",
				},
				{
					Name: "workspace",
				},
			},
			expectedWorkspaceBindings: []tektonapi.WorkspaceBinding{
				{
					Name:   "git-auth",
					Secret: &corev1.SecretVolumeSource{SecretName: "{{ git_auth_secret }}"},
				},
				{
					Name:                "workspace",
					VolumeClaimTemplate: generateVolumeClaimTemplate(),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createWorkspaceBinding(tt.pipelineWorkspaces)
			if !reflect.DeepEqual(got, tt.expectedWorkspaceBindings) {
				t.Errorf("Expected %#v, but received %#v", tt.expectedWorkspaceBindings, got)
			}
		})
	}
}

func TestSlicesIntersection(t *testing.T) {
	tests := []struct {
		in1, in2     []string
		intersection int
	}{
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "b", "c", "d", "e"},
			intersection: 5,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "b", "c", "q", "y"},
			intersection: 3,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"a", "q", "c", "f", "y"},
			intersection: 1,
		},
		{
			in1:          []string{"a", "b", "c", "d", "e"},
			in2:          []string{"f", "b", "c", "d", "e"},
			intersection: 0,
		},
	}
	for _, tt := range tests {
		t.Run("intersection test", func(t *testing.T) {
			got := pkgslices.Intersection(tt.in1, tt.in2)
			if got != tt.intersection {
				t.Errorf("Got slice intersection %d but expected length is %d", got, tt.intersection)
			}
		})
	}
}

// TODO remove after only new model is used
func TestGeneratePACRepositoryOldModel(t *testing.T) {
	getComponent := func(repoUrl string, annotations map[string]string) compapiv1alpha1.Component {
		return compapiv1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testcomponent",
				Namespace:   "workspace-name",
				Annotations: annotations,
			},
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitSource: &compapiv1alpha1.GitSource{
							URL: repoUrl,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name                      string
		repoUrl                   string
		componentAnnotations      map[string]string
		pacConfig                 map[string][]byte
		expectedGitProviderConfig *pacv1alpha1.GitProvider
	}{
		{
			name:    "should create PaC repository for GitHub application",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for GitHub application even if Github webhook configured",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for GitHub webhook",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.com/user/test-component-repository", nil), false),
				},
				URL:  "",
				Type: "github",
			},
		},
		{
			name:    "should create PaC repository for GitHub application on self-hosted GitHub",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted.com",
			},
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and figure out provider URL from source URL",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://github.self-hosted.com",
				Type: "github",
			},
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and use provider URL from annotation",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://github.self-hosted-proxy.com",
				Type: "github",
			},
		},
		{
			name:    "should create PaC repository for GitLab webhook",
			repoUrl: "https://gitlab.com/user/test-component-repository/",
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository/", nil), false),
				},
				URL:  "",
				Type: "gitlab",
			},
		},
		{
			name:    "should create PaC repository for GitLab webhook even if GitHub application configured",
			repoUrl: "https://gitlab.com/user/test-component-repository.git",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository", nil), false),
				},
				Type: "gitlab",
			},
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and figure out provider URL from source URL",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted.com",
				Type: "gitlab",
			},
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "https://gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation that has no protocol",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
		},
		{
			name:    "should create PaC repository for Forgejo webhook with gitea type for PaC compatibility",
			repoUrl: "https://forgejo.example.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "forgejo",
			},
			pacConfig: map[string][]byte{
				"password": []byte("forgejo-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://forgejo.example.com/user/test-component-repository/", nil), false),
				},
				URL:  "https://forgejo.example.com",
				Type: "gitea",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.repoUrl, tt.componentAnnotations)
			secret := &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelinesAsCodeGitHubAppSecretName,
					Namespace: component.Namespace,
				},
				Data: tt.pacConfig,
			}
			pacRepo, err := generatePACRepository(component, secret, nil, false)

			if err != nil {
				t.Errorf("Failed to generate PaC repository object. Cause: %v", err)
			}

			expectedRepo := strings.TrimSuffix(strings.TrimSuffix(tt.repoUrl, ".git"), "/")
			repositoryName, _ := generatePaCRepositoryNameFromGitUrl(expectedRepo)
			if pacRepo.Name != repositoryName {
				t.Errorf("Generated PaC repository must have name based on component's git url")
			}
			if pacRepo.Namespace != component.Namespace {
				t.Errorf("Generated PaC repository must have the same namespace as corresponding component")
			}

			if pacRepo.Spec.URL != expectedRepo {
				t.Errorf("Wrong git repository URL in PaC repository: %s, want %s", pacRepo.Spec.URL, expectedRepo)
			}
			if !reflect.DeepEqual(pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig) {
				t.Errorf("Wrong git provider config in PaC repository: %#v, want %#v", pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig)
			}
		})
	}
}

func TestGeneratePACRepository(t *testing.T) {
	getComponent := func(repoUrl string, annotations map[string]string, repoSettings *compapiv1alpha1.RepositorySettings) compapiv1alpha1.Component {
		component := getComponentData(componentConfig{
			componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
			gitURL:       repoUrl,
			annotations:  annotations,
		})
		if repoSettings != nil {
			component.Spec.RepositorySettings = *repoSettings
		}
		return *component
	}

	tests := []struct {
		name                             string
		repoUrl                          string
		componentAnnotations             map[string]string
		pacConfig                        map[string][]byte
		expectedGitProviderConfig        *pacv1alpha1.GitProvider
		repoSettings                     *compapiv1alpha1.RepositorySettings
		expectedGithubAppTokenScopeRepos *[]string
		expectedCommentStrategy          string
	}{
		{
			name:    "should create PaC repository for GitHub application",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application even if Github webhook configured",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub webhook",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.com/user/test-component-repository", nil, nil), true),
				},
				URL:  "",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application on self-hosted GitHub",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted.com",
			},
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and figure out provider URL from source URL",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://github.self-hosted.com",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitHub webhook and use provider URL from annotation",
			repoUrl: "https://github.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "github",
				GitProviderAnnotationURL:  "https://github.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://github.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://github.self-hosted-proxy.com",
				Type: "github",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitLab webhook",
			repoUrl: "https://gitlab.com/user/test-component-repository/",
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitLab webhook even if GitHub application configured",
			repoUrl: "https://gitlab.com/user/test-component-repository.git",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.com/user/test-component-repository", nil, nil), true),
				},
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and figure out provider URL from source URL",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "https://gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for self-hosted GitLab webhook and use provider URL from annotation that has no protocol",
			repoUrl: "https://gitlab.self-hosted.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "gitlab",
				GitProviderAnnotationURL:  "gitlab.self-hosted-proxy.com",
			},
			pacConfig: map[string][]byte{
				"password": []byte("glpat-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://gitlab.self-hosted.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://gitlab.self-hosted-proxy.com",
				Type: "gitlab",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for Forgejo webhook with gitea type for PaC compatibility",
			repoUrl: "https://forgejo.example.com/user/test-component-repository/",
			componentAnnotations: map[string]string{
				GitProviderAnnotationName: "forgejo",
			},
			pacConfig: map[string][]byte{
				"password": []byte("forgejo-token"),
			},
			expectedGitProviderConfig: &pacv1alpha1.GitProvider{
				Secret: &pacv1alpha1.Secret{
					Name: PipelinesAsCodeGitHubAppSecretName,
					Key:  "password",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: pipelinesAsCodeWebhooksSecretName,
					Key:  getWebhookSecretKeyForComponent(getComponent("https://forgejo.example.com/user/test-component-repository/", nil, nil), true),
				},
				URL:  "https://forgejo.example.com",
				Type: "gitea",
			},
			repoSettings:                     nil,
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings CommentStrategy",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     &compapiv1alpha1.RepositorySettings{CommentStrategy: "disable_all"},
			expectedGithubAppTokenScopeRepos: nil,
			expectedCommentStrategy:          "disable_all",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings GithubAppTokenScopeRepos",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig:        nil,
			repoSettings:                     &compapiv1alpha1.RepositorySettings{GithubAppTokenScopeRepos: []string{"scope1", "scope2"}},
			expectedGithubAppTokenScopeRepos: &[]string{"scope1", "scope2"},
			expectedCommentStrategy:          "",
		},
		{
			name:    "should create PaC repository for GitHub application with repo settings CommentStrategy and GithubAppTokenScopeRepos",
			repoUrl: "https://github.com/user/test-component-repository",
			pacConfig: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			expectedGitProviderConfig: nil,
			repoSettings: &compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "disable_all",
				GithubAppTokenScopeRepos: []string{"scope1", "scope2"}},
			expectedGithubAppTokenScopeRepos: &[]string{"scope1", "scope2"},
			expectedCommentStrategy:          "disable_all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.repoUrl, tt.componentAnnotations, tt.repoSettings)
			secret := &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelinesAsCodeGitHubAppSecretName,
					Namespace: component.Namespace,
				},
				Data: tt.pacConfig,
			}
			pacRepo, err := generatePACRepository(component, secret, &component.Spec.RepositorySettings, true)

			if err != nil {
				t.Errorf("Failed to generate PaC repository object. Cause: %v", err)
			}

			expectedRepo := strings.TrimSuffix(strings.TrimSuffix(tt.repoUrl, ".git"), "/")
			repositoryName, _ := generatePaCRepositoryNameFromGitUrl(expectedRepo)
			if pacRepo.Name != repositoryName {
				t.Errorf("Generated PaC repository must have name based on component's git url")
			}
			if pacRepo.Namespace != component.Namespace {
				t.Errorf("Generated PaC repository must have the same namespace as corresponding component")
			}

			if pacRepo.Spec.URL != expectedRepo {
				t.Errorf("Wrong git repository URL in PaC repository: %s, want %s", pacRepo.Spec.URL, expectedRepo)
			}
			if !reflect.DeepEqual(pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig) {
				t.Errorf("Wrong git provider config in PaC repository: %#v, want %#v", pacRepo.Spec.GitProvider, tt.expectedGitProviderConfig)
			}

			if tt.expectedGithubAppTokenScopeRepos == nil {
				if len(pacRepo.Spec.Settings.GithubAppTokenScopeRepos) != 0 {
					t.Errorf("Wrong settings GithubAppTokenScopeRepos in PaC repository: %#v, want %#v", pacRepo.Spec.Settings.GithubAppTokenScopeRepos, []string{})
				}
			} else if !reflect.DeepEqual(pacRepo.Spec.Settings.GithubAppTokenScopeRepos, *tt.expectedGithubAppTokenScopeRepos) {
				t.Errorf("Wrong settings GithubAppTokenScopeRepos in PaC repository: %#v, want %#v", pacRepo.Spec.Settings.GithubAppTokenScopeRepos, *tt.expectedGithubAppTokenScopeRepos)
			}

			if pacRepo.Spec.Settings.Github.CommentStrategy != tt.expectedCommentStrategy {
				t.Errorf("Wrong settings Github.CommentStrategy in PaC repository: %s, want %s", pacRepo.Spec.Settings.Github.CommentStrategy, tt.expectedCommentStrategy)
			}
			if pacRepo.Spec.Settings.Gitlab.CommentStrategy != tt.expectedCommentStrategy {
				t.Errorf("Wrong settings Gitlab.CommentStrategy in PaC repository: %s, want %s", pacRepo.Spec.Settings.Gitlab.CommentStrategy, tt.expectedCommentStrategy)
			}
		})
	}
}

// TODO remove after only new model is used
func TestPaCRepoAddParamWorkspaceOldModel(t *testing.T) {
	const workspaceName = "someone-tenant"

	pacConfig := map[string][]byte{
		PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
		PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
	}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-secret",
			Namespace: workspaceName,
		},
		Data: pacConfig,
	}

	component := getComponentDataOldModel(componentConfigOldModel{})

	convertCustomParamsToMap := func(repository *pacv1alpha1.Repository) map[string]pacv1alpha1.Params {
		result := map[string]pacv1alpha1.Params{}
		for _, param := range *repository.Spec.Params {
			result[param.Name] = param
		}
		return result
	}

	t.Run("add to Spec.Params", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, false)
		pacRepoAddParamWorkspaceName(repository, workspaceName)

		params := convertCustomParamsToMap(repository)
		param, ok := params[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)
	})

	t.Run("override existing workspace parameter, unset other fields btw", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, false)
		params := []pacv1alpha1.Params{
			{
				Name:      pacCustomParamAppstudioWorkspace,
				Value:     "another_workspace",
				Filter:    "pac.event_type == \"pull_request\"",
				SecretRef: &pacv1alpha1.Secret{},
			},
		}
		repository.Spec.Params = &params

		pacRepoAddParamWorkspaceName(repository, workspaceName)

		existingParams := convertCustomParamsToMap(repository)
		param, ok := existingParams[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)

		assert.Equal(t, "", param.Filter)
		assert.Assert(t, param.SecretRef == nil)
	})
}

func TestPaCRepoAddParamWorkspace(t *testing.T) {
	const workspaceName = "someone-tenant"

	pacConfig := map[string][]byte{
		PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
		PipelinesAsCodeGithubPrivateKey: []byte(ghAppPrivateKeyStub),
	}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-secret",
			Namespace: workspaceName,
		},
		Data: pacConfig,
	}

	component := getComponentData(componentConfig{})

	convertCustomParamsToMap := func(repository *pacv1alpha1.Repository) map[string]pacv1alpha1.Params {
		result := map[string]pacv1alpha1.Params{}
		for _, param := range *repository.Spec.Params {
			result[param.Name] = param
		}
		return result
	}

	t.Run("add to Spec.Params", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, true)
		pacRepoAddParamWorkspaceName(repository, workspaceName)

		params := convertCustomParamsToMap(repository)
		param, ok := params[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)
	})

	t.Run("override existing workspace parameter, unset other fields btw", func(t *testing.T) {
		repository, _ := generatePACRepository(*component, secret, nil, true)
		params := []pacv1alpha1.Params{
			{
				Name:      pacCustomParamAppstudioWorkspace,
				Value:     "another_workspace",
				Filter:    "pac.event_type == \"pull_request\"",
				SecretRef: &pacv1alpha1.Secret{},
			},
		}
		repository.Spec.Params = &params

		pacRepoAddParamWorkspaceName(repository, workspaceName)

		existingParams := convertCustomParamsToMap(repository)
		param, ok := existingParams[pacCustomParamAppstudioWorkspace]
		assert.Assert(t, ok)
		assert.Equal(t, workspaceName, param.Value)

		assert.Equal(t, "", param.Filter)
		assert.Assert(t, param.SecretRef == nil)
	})
}

// TODO remove after only new model is used
func TestGetGitProviderOldModel(t *testing.T) {
	getComponent := func(repoUrl, annotationValue string) compapiv1alpha1.Component {
		componentMeta := metav1.ObjectMeta{
			Name:      "testcomponent",
			Namespace: "workspace-name",
		}
		if annotationValue != "" {
			componentMeta.Annotations = map[string]string{
				GitProviderAnnotationName: annotationValue,
			}
		}

		component := compapiv1alpha1.Component{
			ObjectMeta: componentMeta,
			Spec: compapiv1alpha1.ComponentSpec{
				Source: compapiv1alpha1.ComponentSource{
					ComponentSourceUnion: compapiv1alpha1.ComponentSourceUnion{
						GitSource: &compapiv1alpha1.GitSource{
							URL: repoUrl,
						},
					},
				},
			},
		}
		return component
	}

	tests := []struct {
		name                           string
		componentRepoUrl               string
		componentGitProviderAnnotation string
		want                           string
		expectError                    bool
	}{
		{
			name:             "should detect github provider via http url",
			componentRepoUrl: "https://github.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect github provider via git url",
			componentRepoUrl: "git@github.com:user/test-component-repository",
			expectError:      true,
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via http url",
			componentRepoUrl: "https://cooler.github.my-company.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via git url",
			componentRepoUrl: "git@cooler.github.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider via http url",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider via git url",
			componentRepoUrl: "git@gitlab.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect non-standard gitlab provider via http url",
			componentRepoUrl: "https://cooler.gitlab.my-company.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect non-standard gitlab provider via git url",
			componentRepoUrl: "git@cooler.gitlab.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should detect github provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "github",
			want:                           "github",
		},
		{
			name:                           "should detect gitlab provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:                           "should prefer the annotation over the url",
			componentRepoUrl:               "https://not.github.my-company.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:             "should fail to detect git provider for self-hosted instance if annotation is not set",
			componentRepoUrl: "https://mydomain.com/user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should fail to detect git provider for self-hosted instance if annotation is set to invalid value",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "mylab",
			expectError:                    true,
		},
		{
			name:             "should fail to detect git provider component repository URL is invalid",
			componentRepoUrl: "12345",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL is empty",
			componentRepoUrl: "",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree/branch/file",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git' and slash",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git/",
			want:             "gitlab",
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/tree/main/file",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/commit/shacommit",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/blob/main/blobfile",
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.componentRepoUrl, tt.componentGitProviderAnnotation)
			got, err := getGitProvider(component, false)
			if tt.expectError {
				if err == nil {
					t.Errorf("Detecting git provider for component with '%s' url and '%s' annotation value should fail", tt.componentRepoUrl, tt.componentGitProviderAnnotation)
				}
			} else {
				if got != tt.want {
					t.Errorf("Expected git provider is: %s, but got %s", tt.want, got)
				}
			}
		})
	}

	t.Run("should return error if git source is nil", func(t *testing.T) {
		component := getComponent("", "")
		component.Spec.Source.GitSource = nil
		_, err := getGitProvider(component, false)
		if err == nil {
			t.Error("Expected error for nil source URL")
		}
	})
}

func TestGetGitProvider(t *testing.T) {
	getComponent := func(repoUrl, annotationValue string) compapiv1alpha1.Component {
		component := getComponentData(componentConfig{
			componentKey:             types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
			forceEmptyUrlAndVersions: true,
			gitURL:                   repoUrl,
		})
		if annotationValue != "" {
			component.ObjectMeta.Annotations = map[string]string{
				GitProviderAnnotationName: annotationValue,
			}
		}
		return *component
	}

	tests := []struct {
		name                           string
		componentRepoUrl               string
		componentGitProviderAnnotation string
		want                           string
		expectError                    bool
	}{
		{
			name:             "should detect github provider via https url",
			componentRepoUrl: "https://github.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect github provider via git url",
			componentRepoUrl: "git@github.com:user/test-component-repository",
			expectError:      true,
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via https url",
			componentRepoUrl: "https://cooler.github.my-company.com/user/test-component-repository",
			want:             "github",
		},
		{
			name:             "should detect non-standard github provider via git url",
			componentRepoUrl: "git@cooler.github.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider via https url",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider via git url",
			componentRepoUrl: "git@gitlab.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:             "should detect non-standard gitlab provider via https url",
			componentRepoUrl: "https://cooler.gitlab.my-company.com/user/test-component-repository",
			want:             "gitlab",
		},
		{
			name:             "should detect non-standard gitlab provider via git url",
			componentRepoUrl: "git@cooler.gitlab.my-company.com:user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should detect github provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "github",
			want:                           "github",
		},
		{
			name:                           "should detect gitlab provider via annotation",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:                           "should prefer the annotation over the url",
			componentRepoUrl:               "https://not.github.my-company.com/user/test-component-repository",
			componentGitProviderAnnotation: "gitlab",
			want:                           "gitlab",
		},
		{
			name:             "should fail to detect git provider for self-hosted instance if annotation is not set",
			componentRepoUrl: "https://mydomain.com/user/test-component-repository",
			expectError:      true,
		},
		{
			name:                           "should fail to detect git provider for self-hosted instance if annotation is set to invalid value",
			componentRepoUrl:               "https://mydomain.com/user/test-component-repository",
			componentGitProviderAnnotation: "mylab",
			expectError:                    true,
		},
		{
			name:             "should fail to detect git provider component repository URL is invalid",
			componentRepoUrl: "12345",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL is empty",
			componentRepoUrl: "",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path doesn't have 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree",
			expectError:      true,
		},
		{
			name:             "should return error if git source URL path has more than 2 parts namespace(owner)/repo",
			componentRepoUrl: "https://github.com/user/repository/tree/branch/file",
			expectError:      true,
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider even if path has more than 2 parts",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git",
			want:             "gitlab",
		},
		{
			name:             "should detect gitlab provider url ends with '.git' and slash",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository.git/",
			want:             "gitlab",
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/tree/main/file",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/commit/shacommit",
			expectError:      true,
		},
		{
			name:             "should return error if gitlab url contains '-'",
			componentRepoUrl: "https://gitlab.com/user/test-component-repository/additional/other/-/blob/main/blobfile",
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := getComponent(tt.componentRepoUrl, tt.componentGitProviderAnnotation)
			got, err := getGitProvider(component, true)
			if tt.expectError {
				if err == nil {
					t.Errorf("Detecting git provider for component with '%s' url and '%s' annotation value should fail", tt.componentRepoUrl, tt.componentGitProviderAnnotation)
				}
			} else {
				if got != tt.want {
					t.Errorf("Expected git provider is: %s, but got %s", tt.want, got)
				}
			}
		})
	}

	t.Run("should return error if git source is nil", func(t *testing.T) {
		component := getComponent("", "")
		component.Spec.Source.GitURL = ""
		_, err := getGitProvider(component, true)
		if err == nil {
			t.Error("Expected error for nil source URL")
		}
	})
}

func TestSanitizeVersionName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "should convert to lowercase and replace dots and underscores with hyphens",
			input:    "V1.0_BETA",
			expected: "v1-0-beta",
		},
		{
			name:     "should remove leading and trailing hyphens",
			input:    "__v1.0__",
			expected: "v1-0",
		},
		{
			name:     "should handle empty result after sanitization",
			input:    "___",
			expected: "",
		},
		{
			name:     "should handle already sanitized name",
			input:    "v1-0",
			expected: "v1-0",
		},
		{
			name:     "should remove special characters",
			input:    "v1@#$0",
			expected: "v1---0",
		},
		{
			name:     "should replace underscores with hyphens",
			input:    "v1___0",
			expected: "v1---0",
		},
		{
			name:     "should handle empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeVersionName(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidateVersions(t *testing.T) {
	tests := []struct {
		name        string
		component   *compapiv1alpha1.Component
		expectError bool
		errorSubstr string
	}{
		{
			name: "should accept valid versions",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main"},
					{Name: "v2.0", Revision: "develop"},
				},
			}),
			expectError: false,
		},
		{
			name: "should validate version names are required",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "", Revision: "main"},
				},
			}),
			expectError: true,
			errorSubstr: "spec.source.versions[0].name is required",
		},
		{
			name: "should validate version revisions are required",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: ""},
				},
			}),
			expectError: true,
			errorSubstr: "spec.source.versions[0].revision is required",
		},
		{
			name: "should detect duplicate sanitized version names",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main"},
					{Name: "v1_0", Revision: "develop"},
				},
			}),
			expectError: true,
			errorSubstr: "conflicts with version",
		},
		{
			name: "should detect empty sanitized version name",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "___", Revision: "main"},
				},
			}),
			expectError: true,
			errorSubstr: "becomes empty after sanitization",
		},
		{
			name: "should validate version revisions are required, while 2 versions are valid",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main"},
					{Name: "v2.0", Revision: "develop"},
					{Name: "v3.0", Revision: ""},
				},
			}),
			expectError: true,
			errorSubstr: "spec.source.versions[2].revision is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateVersions(tt.component)
			if tt.expectError {
				assert.Assert(t, len(errors) > 0, "expected errors, got none")
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			} else {
				assert.Equal(t, 0, len(errors), "expected no errors, got %d: %v", len(errors), errors)
			}
		})
	}
}

func TestValidatePipelineConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		pipeline    compapiv1alpha1.ComponentBuildPipeline
		versionName string
		wantErrors  int
		errorSubstr string
	}{
		{
			name: "should validate that PullAndPush cannot be used with Pull or Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should validate that PullAndPush cannot be used with Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should validate PipelineRefGit required fields - missing pathInRepo",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:      "https://github.com/test/pipelines",
						Revision: "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "pathInRepo is required",
		},
		{
			name: "should validate PipelineRefGit required fields - missing url and revision",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						PathInRepo: ".tekton/pipeline.yaml",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  2,
			errorSubstr: "is required",
		},
		{
			name: "should validate PipelineSpecFromBundle required fields",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
						Bundle: "quay.io/repo/bundle:latest",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "name is required",
		},
		{
			name: "should validate multiple definition types cannot be used together",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:        "https://github.com/test/pipelines",
						PathInRepo: ".tekton/pipeline.yaml",
						Revision:   "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  1,
			errorSubstr: "has multiple definitions specified",
		},
		{
			name:        "should accept empty pipeline configuration",
			pipeline:    compapiv1alpha1.ComponentBuildPipeline{},
			versionName: "test-version",
			wantErrors:  0,
		},
		{
			name: "should accept valid pipeline with PipelineRefName",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			versionName: "test-version",
			wantErrors:  0,
		},
		{
			name: "should accept valid pipeline with PullAndPush using PipelineRefGit",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefGit: compapiv1alpha1.PipelineRefGit{
						Url:        "https://github.com/test/pipelines",
						PathInRepo: ".tekton/pipeline.yaml",
						Revision:   "main",
					},
				},
			},
			versionName: "test-version",
			wantErrors:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineConfiguration(tt.pipeline, tt.versionName)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestBuildVersionInfoMap(t *testing.T) {
	tests := []struct {
		name       string
		component  *compapiv1alpha1.Component
		fromStatus bool
		validate   func(t *testing.T, result map[string]*VersionInfo)
	}{
		{
			name: "should build version info map from spec with all fields",
			component: getComponentData(componentConfig{
				componentKey:  types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				dockerfileURI: "Dockerfile.default",
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main", Context: "app1"},
					{Name: "v2.0", Revision: "develop", DockerfileURI: "Dockerfile.custom"},
				},
			}),
			fromStatus: false,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				assert.Equal(t, "app1", result["v1.0"].Context)
				assert.Equal(t, "Dockerfile.default", result["v1.0"].DockerfileURI)

				assert.Equal(t, "v2.0", result["v2.0"].OriginalVersion)
				assert.Equal(t, "Dockerfile.custom", result["v2.0"].DockerfileURI)
			},
		},
		{
			name: "should build version info map from spec without DockerfileURI and default to Dockerfile",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1.0", Revision: "main"},
					{Name: "v2.0", Revision: "develop", Context: "app2"},
				},
			}),
			fromStatus: false,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				assert.Equal(t, "Dockerfile", result["v1.0"].DockerfileURI)

				assert.Equal(t, "v2.0", result["v2.0"].OriginalVersion)
				assert.Equal(t, "Dockerfile", result["v2.0"].DockerfileURI)
				assert.Equal(t, "app2", result["v2.0"].Context)
			},
		},
		{
			name: "should build version info map from status",
			component: &compapiv1alpha1.Component{
				Status: compapiv1alpha1.ComponentStatus{
					Versions: []compapiv1alpha1.ComponentVersionStatus{
						{Name: "v1.0", Revision: "main"},
						{Name: "v2.0", Revision: "develop"},
					},
				},
			},
			fromStatus: true,
			validate: func(t *testing.T, result map[string]*VersionInfo) {
				assert.Equal(t, 2, len(result))
				assert.Equal(t, "v1.0", result["v1.0"].OriginalVersion)
				assert.Equal(t, "v1-0", result["v1.0"].SanitizedVersion)
				assert.Equal(t, "main", result["v1.0"].Revision)
				// Status-based map should not include Context or DockerfileURI
				assert.Equal(t, "", result["v1.0"].Context)
				assert.Equal(t, "", result["v1.0"].DockerfileURI)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildVersionInfoMap(tt.component, tt.fromStatus)
			tt.validate(t, result)
		})
	}
}

func TestEqualRepositorySettings(t *testing.T) {
	tests := []struct {
		name      string
		settings1 compapiv1alpha1.RepositorySettings
		settings2 compapiv1alpha1.RepositorySettings
		expected  bool
	}{
		{
			name: "should detect different comment strategies",
			settings1: compapiv1alpha1.RepositorySettings{
				CommentStrategy: "",
			},
			settings2: compapiv1alpha1.RepositorySettings{
				CommentStrategy: "disable_all",
			},
			expected: false,
		},
		{
			name: "should detect different GitHub app token scope repos",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1"},
			},
			expected: false,
		},
		{
			name: "should consider different ordering of GitHub app token scope repos as equal",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo2", "repo1"},
			},
			expected: true,
		},
		{
			name: "should ignore duplicates when comparing GitHub app token scope repos",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo2", "repo1"},
			},
			expected: true,
		},
		{
			name: "should detect different repos despite duplicates",
			settings1: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				GithubAppTokenScopeRepos: []string{"repo1", "repo3", "repo3"},
			},
			expected: false,
		},
		{
			name: "should consider equal settings as equal",
			settings1: compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "",
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			settings2: compapiv1alpha1.RepositorySettings{
				CommentStrategy:          "",
				GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
			},
			expected: true,
		},
		{
			name:      "should consider empty settings as equal",
			settings1: compapiv1alpha1.RepositorySettings{},
			settings2: compapiv1alpha1.RepositorySettings{},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalRepositorySettings(tt.settings1, tt.settings2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetVersionsForAction(t *testing.T) {
	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name             string
		allVersions      bool
		singleVersion    string
		multipleVersions []string
		wantValid        []string
		wantInvalid      []string
	}{
		{
			name:        "should return all versions when AllVersions is true",
			allVersions: true,
			wantValid:   []string{"v1", "v2"},
			wantInvalid: []string{},
		},
		{
			name:             "should return all versions when AllVersions is true but still check for invalid versions",
			allVersions:      true,
			singleVersion:    "invalid1",
			multipleVersions: []string{"v2", "invalid2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{"invalid1", "invalid2"},
		},
		{
			name:          "should return single version when specified",
			singleVersion: "v1",
			wantValid:     []string{"v1"},
			wantInvalid:   []string{},
		},
		{
			name:             "should return multiple versions when specified",
			multipleVersions: []string{"v1", "v2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{},
		},
		{
			name:             "should filter out invalid versions",
			multipleVersions: []string{"v1", "v3", "v4"},
			wantValid:        []string{"v1"},
			wantInvalid:      []string{"v3", "v4"},
		},
		{
			name:             "should remove duplicates and combine single and multiple",
			singleVersion:    "v1",
			multipleVersions: []string{"v1", "v2"},
			wantValid:        []string{"v1", "v2"},
			wantInvalid:      []string{},
		},
		{
			name:          "should identify invalid single version",
			singleVersion: "nonexistent",
			wantValid:     []string{},
			wantInvalid:   []string{"nonexistent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validVersions, invalidVersions := getUniqueVersionsFromVersionFields(
				tt.allVersions,
				tt.singleVersion,
				tt.multipleVersions,
				existingVersions,
			)

			assert.Equal(t, len(tt.wantValid), len(validVersions), "valid versions count mismatch")
			assert.Equal(t, len(tt.wantInvalid), len(invalidVersions), "invalid versions count mismatch")

			for _, v := range tt.wantValid {
				assert.Assert(t, slices.Contains(validVersions, v), "expected valid version %s not found", v)
			}
			for _, v := range tt.wantInvalid {
				assert.Assert(t, slices.Contains(invalidVersions, v), "expected invalid version %s not found", v)
			}
		})
	}
}

func TestHasPipelineRefGit(t *testing.T) {
	tests := []struct {
		name     string
		refGit   compapiv1alpha1.PipelineRefGit
		expected bool
	}{
		{
			name: "should return true when all fields set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			expected: true,
		},
		{
			name: "should return true when only url set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url: "https://github.com/test/repo",
			},
			expected: true,
		},
		{
			name:     "should return false when all fields empty",
			refGit:   compapiv1alpha1.PipelineRefGit{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineRefGit(tt.refGit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineRefName(t *testing.T) {
	tests := []struct {
		name     string
		refName  string
		expected bool
	}{
		{
			name:     "should return true for non-empty name",
			refName:  "docker-build",
			expected: true,
		},
		{
			name:     "should return false for empty name",
			refName:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineRefName(tt.refName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineSpecFromBundle(t *testing.T) {
	tests := []struct {
		name           string
		specFromBundle compapiv1alpha1.PipelineSpecFromBundle
		expected       bool
	}{
		{
			name: "should return true when both fields set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
				Name:   "docker-build",
			},
			expected: true,
		},
		{
			name: "should return true when only bundle set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
			},
			expected: true,
		},
		{
			name:           "should return false when all fields empty",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{},
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineSpecFromBundle(tt.specFromBundle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidatePipelineRefGitFields(t *testing.T) {
	tests := []struct {
		name        string
		refGit      compapiv1alpha1.PipelineRefGit
		errorPrefix string
		wantErrors  int
		errorSubstr string
	}{
		{
			name: "should accept all fields set",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			errorPrefix: "test",
			wantErrors:  0,
		},
		{
			name: "should reject missing url",
			refGit: compapiv1alpha1.PipelineRefGit{
				PathInRepo: ".tekton/pipeline.yaml",
				Revision:   "main",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "url is required",
		},
		{
			name: "should reject missing pathInRepo",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:      "https://github.com/test/repo",
				Revision: "main",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "pathInRepo is required",
		},
		{
			name: "should reject missing revision",
			refGit: compapiv1alpha1.PipelineRefGit{
				Url:        "https://github.com/test/repo",
				PathInRepo: ".tekton/pipeline.yaml",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "revision is required",
		},
		{
			name:        "should reject all missing fields",
			refGit:      compapiv1alpha1.PipelineRefGit{},
			errorPrefix: "version v1",
			wantErrors:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineRefGitFields(tt.refGit, tt.errorPrefix)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestValidatePipelineSpecFromBundleFields(t *testing.T) {
	tests := []struct {
		name           string
		specFromBundle compapiv1alpha1.PipelineSpecFromBundle
		errorPrefix    string
		wantErrors     int
		errorSubstr    string
	}{
		{
			name: "should accept both fields set",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
				Name:   "docker-build",
			},
			errorPrefix: "test",
			wantErrors:  0,
		},
		{
			name: "should reject missing bundle",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Name: "docker-build",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "bundle is required",
		},
		{
			name: "should reject missing name",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
				Bundle: "quay.io/repo/bundle:latest",
			},
			errorPrefix: "test",
			wantErrors:  1,
			errorSubstr: "name is required",
		},
		{
			name:           "should reject all missing fields",
			specFromBundle: compapiv1alpha1.PipelineSpecFromBundle{},
			errorPrefix:    "version v1",
			wantErrors:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validatePipelineSpecFromBundleFields(tt.specFromBundle, tt.errorPrefix)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
		})
	}
}

func TestHasPipelineDefConfig(t *testing.T) {
	tests := []struct {
		name        string
		pipelineDef compapiv1alpha1.PipelineDefinition
		expected    bool
	}{
		{
			name: "should return true for PipelineRefGit",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefGit: compapiv1alpha1.PipelineRefGit{
					Url: "https://github.com/test/repo",
				},
			},
			expected: true,
		},
		{
			name: "should return true for PipelineRefName",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefName: "docker-build",
			},
			expected: true,
		},
		{
			name: "should return true for PipelineSpecFromBundle",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
					Bundle: "quay.io/repo/bundle:latest",
				},
			},
			expected: true,
		},
		{
			name:        "should return false for empty definition",
			pipelineDef: compapiv1alpha1.PipelineDefinition{},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineDefConfig(tt.pipelineDef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPipelineConfig(t *testing.T) {
	tests := []struct {
		name     string
		pipeline compapiv1alpha1.ComponentBuildPipeline
		expected bool
	}{
		{
			name: "should return true for PullAndPush",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				PullAndPush: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name: "should return true for Pull",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Pull: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name: "should return true for Push",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{
				Push: compapiv1alpha1.PipelineDefinition{
					PipelineRefName: "docker-build",
				},
			},
			expected: true,
		},
		{
			name:     "should return false for empty pipeline",
			pipeline: compapiv1alpha1.ComponentBuildPipeline{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPipelineConfig(tt.pipeline)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPipelineDef(t *testing.T) {
	tests := []struct {
		name        string
		pipelineDef compapiv1alpha1.PipelineDefinition
		validate    func(t *testing.T, result *PipelineDef)
	}{
		{
			name: "should extract PipelineRefGit",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefGit: compapiv1alpha1.PipelineRefGit{
					Url:        "https://github.com/test/repo",
					PathInRepo: ".tekton/pipeline.yaml",
					Revision:   "main",
				},
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Assert(t, result.PipelineRefGit != nil)
				assert.Equal(t, "https://github.com/test/repo", result.PipelineRefGit.Url)
				assert.Equal(t, "", result.PipelineRefName)
				assert.Assert(t, result.PipelineSpecFromBundle == nil)
			},
		},
		{
			name: "should extract PipelineRefName",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineRefName: "docker-build",
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Equal(t, "docker-build", result.PipelineRefName)
				assert.Assert(t, result.PipelineRefGit == nil)
				assert.Assert(t, result.PipelineSpecFromBundle == nil)
			},
		},
		{
			name: "should extract PipelineSpecFromBundle",
			pipelineDef: compapiv1alpha1.PipelineDefinition{
				PipelineSpecFromBundle: compapiv1alpha1.PipelineSpecFromBundle{
					Bundle: "quay.io/repo/bundle:latest",
					Name:   "docker-build",
				},
			},
			validate: func(t *testing.T, result *PipelineDef) {
				assert.Assert(t, result.PipelineSpecFromBundle != nil)
				assert.Equal(t, "quay.io/repo/bundle:latest", result.PipelineSpecFromBundle.Bundle)
				assert.Assert(t, result.PipelineRefGit == nil)
				assert.Equal(t, "", result.PipelineRefName)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPipelineDef(tt.pipelineDef)
			tt.validate(t, result)
		})
	}
}

func TestValidatePipelines(t *testing.T) {
	tests := []struct {
		name              string
		component         *compapiv1alpha1.Component
		wantErrors        int
		wantPipelineCount int
		errorSubstr       string
		wantPipelines     map[string]*VersionPipelineDefinition
	}{
		{
			name: "should validate and extract pipelines from default and versions",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					Pull: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "docker-build",
					},
				},
			}),
			wantErrors:        0,
			wantPipelineCount: 2, // both versions inherit from default
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "docker-build"}, // from default
					Push: nil,
				},
				"v2": {
					Pull: &PipelineDef{PipelineRefName: "docker-build"}, // from default
					Push: nil,
				},
			},
		},
		{
			name: "should merge default pipeline with version-specific pipeline",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{
						Name:     "v1",
						Revision: "main",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Push: compapiv1alpha1.PipelineDefinition{
								PipelineRefName: "custom-push",
							},
						},
					},
					{Name: "v2", Revision: "develop"},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					Pull: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "default-pull",
					},
				},
			}),
			wantErrors:        0,
			wantPipelineCount: 2,
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "default-pull"}, // from default
					Push: &PipelineDef{PipelineRefName: "custom-push"},  // from version
				},
				"v2": {
					Pull: &PipelineDef{PipelineRefName: "default-pull"}, // from default
					Push: nil,
				},
			},
		},
		{
			name: "should handle default PullAndPush with version-specific Pull, Push, and PullAndPush",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{
						Name:     "v1",
						Revision: "main",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Pull: compapiv1alpha1.PipelineDefinition{
								PipelineRefName: "custom-pull",
							},
						},
					},
					{
						Name:     "v2",
						Revision: "develop",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Push: compapiv1alpha1.PipelineDefinition{
								PipelineRefName: "custom-push",
							},
						},
					},
					{
						Name:     "v3",
						Revision: "release",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							PullAndPush: compapiv1alpha1.PipelineDefinition{
								PipelineRefName: "custom-pullandpush",
							},
						},
					},
					{Name: "v4", Revision: "staging"},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					PullAndPush: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "default-pullandpush",
					},
				},
			}),
			wantErrors:        0,
			wantPipelineCount: 4,
			wantPipelines: map[string]*VersionPipelineDefinition{
				"v1": {
					Pull: &PipelineDef{PipelineRefName: "custom-pull"}, // from version
					Push: nil,                                          // default PullAndPush ignored when version has Pull/Push
				},
				"v2": {
					Pull: nil,                                          // default PullAndPush ignored when version has Pull/Push
					Push: &PipelineDef{PipelineRefName: "custom-push"}, // from version
				},
				"v3": {
					Pull: &PipelineDef{PipelineRefName: "custom-pullandpush"}, // from version PullAndPush
					Push: &PipelineDef{PipelineRefName: "custom-pullandpush"}, // from version PullAndPush (same as Pull)
				},
				"v4": {
					Pull: &PipelineDef{PipelineRefName: "default-pullandpush"}, // from default PullAndPush
					Push: &PipelineDef{PipelineRefName: "default-pullandpush"}, // from default PullAndPush (same as Pull)
				},
			},
		},
		{
			name: "should detect validation errors in default pipeline",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
				defaultPipeline: compapiv1alpha1.ComponentBuildPipeline{
					PullAndPush: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "docker-build",
					},
					Pull: compapiv1alpha1.PipelineDefinition{
						PipelineRefName: "docker-build",
					},
				},
			}),
			wantErrors:  1,
			errorSubstr: "cannot specify pull-and-push together with pull or push",
		},
		{
			name: "should detect validation errors in version-specific pipeline",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{
						Name:     "v1",
						Revision: "main",
						BuildPipeline: compapiv1alpha1.ComponentBuildPipeline{
							Pull: compapiv1alpha1.PipelineDefinition{
								PipelineRefGit: compapiv1alpha1.PipelineRefGit{
									Url: "https://github.com/test/repo",
									// Missing required fields
								},
							},
						},
					},
				},
			}),
			wantErrors:  2, // missing pathInRepo and revision
			errorSubstr: "is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors, pipelines := validatePipelines(tt.component)
			assert.Equal(t, tt.wantErrors, len(errors), "expected %d errors, got %d: %v", tt.wantErrors, len(errors), errors)
			if tt.wantErrors > 0 && tt.errorSubstr != "" {
				found := false
				for _, err := range errors {
					if strings.Contains(err, tt.errorSubstr) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "expected error containing '%s', got: %v", tt.errorSubstr, errors)
			}
			if tt.wantErrors == 0 {
				assert.Equal(t, tt.wantPipelineCount, len(pipelines), "expected %d pipelines, got %d", tt.wantPipelineCount, len(pipelines))
				if tt.wantPipelines != nil {
					for versionName, wantPipeline := range tt.wantPipelines {
						gotPipeline := pipelines[versionName]
						assert.Assert(t, gotPipeline != nil, "pipeline for version %s should exist", versionName)

						// Compare Pull pipeline
						if wantPipeline.Pull == nil {
							assert.Assert(t, gotPipeline.Pull == nil, "version %s Pull should be nil", versionName)
						} else {
							assert.Assert(t, gotPipeline.Pull != nil, "version %s Pull should not be nil", versionName)
							assert.Equal(t, wantPipeline.Pull.PipelineRefName, gotPipeline.Pull.PipelineRefName, "version %s Pull pipeline mismatch", versionName)
						}

						// Compare Push pipeline
						if wantPipeline.Push == nil {
							assert.Assert(t, gotPipeline.Push == nil, "version %s Push should be nil", versionName)
						} else {
							assert.Assert(t, gotPipeline.Push != nil, "version %s Push should not be nil", versionName)
							assert.Equal(t, wantPipeline.Push.PipelineRefName, gotPipeline.Push.PipelineRefName, "version %s Push pipeline mismatch", versionName)
						}
					}
				}
			}
		})
	}
}

func TestDetermineVersionsToCreateConfiguration(t *testing.T) {
	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name                             string
		component                        *compapiv1alpha1.Component
		wantVersionsToCreatePRFor        []string
		wantInvalidVersionsToCreatePRFor []string
	}{
		{
			name: "should return all versions with AllVersions flag",
			component: getComponentData(componentConfig{
				componentKey:             types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				forceEmptyUrlAndVersions: true,
				actions:                  compapiv1alpha1.ComponentActions{CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{AllVersions: true}},
			}),
			wantVersionsToCreatePRFor:        []string{"v1", "v2"},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
		{
			name: "should return version from version",
			component: getComponentData(componentConfig{
				componentKey:             types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				forceEmptyUrlAndVersions: true,
				actions:                  compapiv1alpha1.ComponentActions{CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{Version: "v1"}},
			}),
			wantVersionsToCreatePRFor:        []string{"v1"},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
		{
			name: "should filter invalid versions",
			component: getComponentData(componentConfig{
				componentKey:             types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				forceEmptyUrlAndVersions: true,
				actions:                  compapiv1alpha1.ComponentActions{CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{Versions: []string{"v1", "v3"}}},
			}),
			wantVersionsToCreatePRFor:        []string{"v1"},
			wantInvalidVersionsToCreatePRFor: []string{"v3"},
		},
		{
			name: "should return empty when no action specified",
			component: getComponentData(componentConfig{
				componentKey:             types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				forceEmptyUrlAndVersions: true,
				actions:                  compapiv1alpha1.ComponentActions{CreateConfiguration: compapiv1alpha1.ComponentCreatePipelineConfiguration{}},
			}),
			wantVersionsToCreatePRFor:        []string{},
			wantInvalidVersionsToCreatePRFor: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			versionsToCreatePRFor, invalidVersionsToCreatePRFor := reconciler.determineVersionsToCreateConfigurationFor(context.TODO(), tt.component, existingVersions)

			assert.Equal(t, len(tt.wantVersionsToCreatePRFor), len(versionsToCreatePRFor), "versions to create PR for count mismatch")
			assert.Equal(t, len(tt.wantInvalidVersionsToCreatePRFor), len(invalidVersionsToCreatePRFor), "invalid versions to create PR for count mismatch")

			for _, v := range tt.wantVersionsToCreatePRFor {
				assert.Assert(t, slices.Contains(versionsToCreatePRFor, v), "expected version to create PR for %s not found", v)
			}
			for _, v := range tt.wantInvalidVersionsToCreatePRFor {
				assert.Assert(t, slices.Contains(invalidVersionsToCreatePRFor, v), "expected invalid version to create PR for %s not found", v)
			}
		})
	}
}

func TestDetermineVersionsToOnboardAndOffboard(t *testing.T) {
	tests := []struct {
		name                   string
		component              *compapiv1alpha1.Component
		wantVersionsToOnboard  []string
		wantVersionsToOffboard []string
	}{
		{
			name: "should identify versions to onboard",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"},
				},
				status: compapiv1alpha1.ComponentStatus{Versions: []compapiv1alpha1.ComponentVersionStatus{{Name: "v1", Revision: "main"}}},
			}),
			wantVersionsToOnboard:  []string{"v2"},
			wantVersionsToOffboard: []string{},
		},
		{
			name: "should identify all versions to onboard when status is empty",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"},
				},
				status: compapiv1alpha1.ComponentStatus{Versions: []compapiv1alpha1.ComponentVersionStatus{}},
			}),
			wantVersionsToOnboard:  []string{"v1", "v2"},
			wantVersionsToOffboard: []string{},
		},
		{
			name: "should identify versions to offboard",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
				},
				status: compapiv1alpha1.ComponentStatus{Versions: []compapiv1alpha1.ComponentVersionStatus{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"}}},
			}),
			wantVersionsToOnboard:  []string{},
			wantVersionsToOffboard: []string{"v2"},
		},
		{
			name: "should identify both versions to onboard and offboard simultaneously",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				versions: []compapiv1alpha1.ComponentVersion{
					{Name: "v1", Revision: "main"},
					{Name: "v3", Revision: "feature"},
				},
				status: compapiv1alpha1.ComponentStatus{Versions: []compapiv1alpha1.ComponentVersionStatus{
					{Name: "v1", Revision: "main"},
					{Name: "v2", Revision: "develop"}}},
			}),
			wantVersionsToOnboard:  []string{"v3"},
			wantVersionsToOffboard: []string{"v2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			existingSpecVersions := buildVersionInfoMap(tt.component, false)
			versionsToOnboard, versionsToOffboard := reconciler.determineVersionsToOnboardAndOffboard(
				context.TODO(), tt.component, existingSpecVersions,
			)

			assert.Equal(t, len(tt.wantVersionsToOnboard), len(versionsToOnboard), "versions to onboard count mismatch")
			assert.Equal(t, len(tt.wantVersionsToOffboard), len(versionsToOffboard), "versions to offboard count mismatch")

			for _, v := range tt.wantVersionsToOnboard {
				assert.Assert(t, slices.Contains(versionsToOnboard, v), "expected version to onboard %s not found", v)
			}
			for _, v := range tt.wantVersionsToOffboard {
				assert.Assert(t, slices.Contains(versionsToOffboard, v), "expected version to offboard %s not found", v)
			}
		})
	}
}

func TestDetermineVersionsToTriggerBuild(t *testing.T) {
	ctx := context.Background()

	existingVersions := map[string]*VersionInfo{
		"v1": {OriginalVersion: "v1", SanitizedVersion: "v1", Revision: "main"},
		"v2": {OriginalVersion: "v2", SanitizedVersion: "v2", Revision: "develop"},
	}

	tests := []struct {
		name                                 string
		component                            *compapiv1alpha1.Component
		versionsToOnboard                    []string
		versionsToCreatePRFor                []string
		wantVersionsToTriggerBuildFor        []string
		wantInvalidVersionsToTriggerBuildFor []string
	}{
		{
			name: "should filter out versions being onboarded",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				actions:      compapiv1alpha1.ComponentActions{TriggerBuilds: []string{"v1", "v2"}},
			}),
			versionsToOnboard:                    []string{"v1"},
			versionsToCreatePRFor:                []string{},
			wantVersionsToTriggerBuildFor:        []string{"v2"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v1"},
		},
		{
			name: "should filter out versions being created with configuration",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				actions:      compapiv1alpha1.ComponentActions{TriggerBuilds: []string{"v1", "v2"}},
			}),
			versionsToOnboard:                    []string{},
			versionsToCreatePRFor:                []string{"v2"},
			wantVersionsToTriggerBuildFor:        []string{"v1"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v2"},
		},
		{
			name: "should filter out invalid versions",
			component: getComponentData(componentConfig{
				componentKey: types.NamespacedName{Namespace: "workspace-name", Name: "testcomponent"},
				actions:      compapiv1alpha1.ComponentActions{TriggerBuilds: []string{"v1", "v3"}},
			}),
			versionsToOnboard:                    []string{},
			versionsToCreatePRFor:                []string{},
			wantVersionsToTriggerBuildFor:        []string{"v1"},
			wantInvalidVersionsToTriggerBuildFor: []string{"v3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ComponentBuildReconciler{}
			versionsToTriggerBuildFor, invalidVersionsToTriggerBuildFor := reconciler.determineVersionsToTriggerBuildFor(
				ctx, tt.component, existingVersions, tt.versionsToOnboard, tt.versionsToCreatePRFor,
			)

			assert.Equal(t, len(tt.wantVersionsToTriggerBuildFor), len(versionsToTriggerBuildFor), "versions to trigger build for count mismatch")
			assert.Equal(t, len(tt.wantInvalidVersionsToTriggerBuildFor), len(invalidVersionsToTriggerBuildFor), "invalid versions to trigger build for count mismatch")

			for _, v := range tt.wantVersionsToTriggerBuildFor {
				assert.Assert(t, slices.Contains(versionsToTriggerBuildFor, v), "expected version to trigger build for %s not found", v)
			}
			for _, v := range tt.wantInvalidVersionsToTriggerBuildFor {
				assert.Assert(t, slices.Contains(invalidVersionsToTriggerBuildFor, v), "expected invalid version to trigger build for %s not found", v)
			}
		})
	}
}
