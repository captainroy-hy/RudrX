package helm

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ghodss/yaml"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	helmapi "github.com/oam-dev/kubevela/pkg/appfile/helm/apis"
)

type HelmReleaseSpecTemplate = helmapi.HelmReleaseSpec

type HelmRepositorySpecTemplate = helmapi.HelmRepositorySpec

type HelmReleaseTemplate struct {
	// The name or path the Helm chart is available at in the SourceRef.
	Chart string `json:"chart"`

	// Version semver expression, ingnored for charts from GitRepository and Bucket.
	Version string `json:"version,omitempty"`

	// Interval at which check the source for updates. Default 5m.
	Interval *metav1.Duration `json:"interval,omitempty"`

	// Values holds the values for this Helm release
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
}

type HelmRepoTemplate struct {
	// The Helm repository URL, a valid URL contains at least a protocol and host.
	URL string `json:"url"`

	// Interval at which check the upstream for updates. Default 5m.
	Interval *metav1.Duration `json:"interval,omitempty"`
}

func GenerateHelmReleaseAndHelmRepo(helmSpecStr string, svcName, appName, ns string, values map[string]interface{}) (helmRls, helmRepo *unstructured.Unstructured, err error) {
	defaultIntervalDuration := &metav1.Duration{Duration: 5 * time.Minute}

	helmModule := &helmapi.HelmSpec{}
	if err := yaml.Unmarshal([]byte(helmSpecStr), helmModule); err != nil {
		return nil, nil, err
	}

	// construct HelmRepository data
	helmRepo = &unstructured.Unstructured{}
	helmRepo.SetGroupVersionKind(helmapi.HelmRepositoryGVK)
	helmRepo.SetNamespace(ns)
	repoName := fmt.Sprintf("%s-%s-repo", appName, svcName)
	helmRepo.SetName(repoName)

	if helmModule.HelmRepositorySpec.Interval == nil {
		helmModule.HelmRepositorySpec.Interval = defaultIntervalDuration
	}
	helmRepoSpecData := make(map[string]interface{})
	bts, err := json.Marshal(helmModule.HelmRepositorySpec)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(bts, &helmRepoSpecData); err != nil {
		return nil, nil, err
	}
	_ = unstructured.SetNestedMap(helmRepo.Object, helmRepoSpecData, "spec")

	// construct HelmRelease data
	rlsName := fmt.Sprintf("%s-%s-rls", appName, svcName)
	helmRls = &unstructured.Unstructured{}
	helmRls.SetGroupVersionKind(helmapi.HelmReleaseGVK)
	helmRls.SetNamespace(ns)
	helmRls.SetName(rlsName)

	if helmModule.HelmReleaseSpec.Interval == nil {
		helmModule.HelmReleaseSpec.Interval = defaultIntervalDuration
	}

	chartValues := map[string]interface{}{}
	if helmModule.HelmReleaseSpec.Values != nil {
		if err := json.Unmarshal(helmModule.HelmReleaseSpec.Values.Raw, &chartValues); err != nil {
			return nil, nil, err
		}
	}
	for k, v := range values {
		// overrid values with settings from application
		chartValues[k] = v
	}
	if len(chartValues) > 0 {
		// avoid an empty map
		vJSON, _ := json.Marshal(chartValues)
		helmModule.HelmReleaseSpec.Values = &apiextensionsv1.JSON{Raw: vJSON}
	}

	helmModule.HelmReleaseSpec.Chart.Spec.SourceRef = helmapi.CrossNamespaceObjectReference{
		Kind:      "HelmRepository",
		Namespace: ns,
		Name:      repoName,
	}
	helmRlsSpecData := make(map[string]interface{})
	bts, err = json.Marshal(helmModule.HelmReleaseSpec)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(bts, &helmRlsSpecData); err != nil {
		return nil, nil, err
	}
	_ = unstructured.SetNestedMap(helmRls.Object, helmRlsSpecData, "spec")

	return helmRls, helmRepo, nil
}
