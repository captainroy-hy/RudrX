package helm

import (
	"fmt"

	helmctl "github.com/fluxcd/helm-controller/api/v2beta1"
	srcctl "github.com/fluxcd/source-controller/api/v1beta1"
	"github.com/pkg/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	HelmRepositoryKind   string = "helmrepository"
	GitRepositoryKind           = "gitrepository"
	BucketRepositoryKind        = "bucket"
)

type HelmReleaseSpec = helmctl.HelmReleaseSpec

type HelmRepositorySpec = srcctl.HelmRepositorySpec

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

type GitRepoTemplate struct {
	// The Git repository URL, a valid URL contains at least a protocol and host.
	URL string `json:"url"`

	// Interval at which check the upstream for updates. Default 5m.
	Interval *metav1.Duration `json:"interval,omitempty"`
}

type BucketTemplate struct {
	// The bucket name.
	BucketName string `json:"bucketName"`

	// The bucket endpoint address.
	Endpoint string `json:"endpoint"`

	// Interval at which check the upstream for updates. Default 5m.
	Interval *metav1.Duration `json:"interval,omitempty"`
}

// GenerateHelmReleaseAndHelmRepo generates fluxcd CR, HelmRelease and HelmRepository in unstructured format.
func GenerateHelmReleaseAndHelmRepo(rlsUnstruct, repoUnstruct *unstructured.Unstructured, name, ns string, values map[string]interface{}) (release, repo *unstructured.Unstructured, err error) {
	var rlsTemp HelmReleaseTemplate
	var repoTemp HelmRepoTemplate
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(rlsUnstruct.UnstructuredContent(), &rlsTemp)
	if err != nil {
		return nil, nil, err
	}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(repoUnstruct.UnstructuredContent(), &repoTemp)
	if err != nil {
		return nil, nil, err
	}

	// repoName will be used in HelmRepository and HelmChartTemplateSpec both
	repoName := generateHelmRepoName(name, ns)
	// map to HelmRepository
	repo, err = generateHelmRepo(repoTemp)
	if err != nil {
		return nil, nil, errors.Wrap(err, "cannot generate HelmRepository")
	}
	repo.SetName(repoName)
	repo.SetNamespace(ns)

	// map to CrossNamespaceObjectReference
	repoRef := unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	unstructured.SetNestedField(repoRef.Object, "HelmRepository", "kind")
	unstructured.SetNestedField(repoRef.Object, repoName, "name")
	unstructured.SetNestedField(repoRef.Object, ns, "namespace")

	// map to HelmChartTemplateSpec
	chartTemplateSpec := unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	unstructured.SetNestedMap(chartTemplateSpec.Object, repoRef.Object, "sourceRef")
	if rlsTemp.Chart == "" {
		return nil, nil, errors.Errorf("chart name or path is required \n releaseTemplateUnstructure %#v \n rlsTemp %#v \n", rlsUnstruct, rlsTemp)
	}
	unstructured.SetNestedField(chartTemplateSpec.Object, rlsTemp.Chart, "chart")
	if rlsTemp.Version != "" {
		unstructured.SetNestedField(chartTemplateSpec.Object, rlsTemp.Version, "version")
	}

	// map to HelmReleaseSpec
	releaseSpec := unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	unstructured.SetNestedMap(releaseSpec.Object, chartTemplateSpec.Object, "chart", "spec")
	if rlsTemp.Interval != nil {
		unstructured.SetNestedField(releaseSpec.Object, rlsTemp.Interval.ToUnstructured(), "interval")
	} else {
		unstructured.SetNestedField(releaseSpec.Object, "5m", "interval")
	}
	unstructured.SetNestedMap(releaseSpec.Object, values, "values", "raw")

	// map to HelmRelease
	release = &unstructured.Unstructured{}
	release.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "helm.toolkit.fluxcd.io",
		Version: "v2beta1",
		Kind:    "HelmRelease",
	})
	release.SetName(name)
	release.SetNamespace(ns)
	unstructured.SetNestedMap(release.Object, releaseSpec.Object, "spec")

	return release, repo, nil
}

func generateHelmRepo(t HelmRepoTemplate) (*unstructured.Unstructured, error) {
	if t.URL == "" {
		return nil, errors.New("name of HelmRepository is required")
	}
	repoSpec := unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	unstructured.SetNestedField(repoSpec.Object, t.URL, "url")
	if t.Interval != nil {
		unstructured.SetNestedField(repoSpec.Object, t.Interval.ToUnstructured(), "interval")
	} else {
		unstructured.SetNestedField(repoSpec.Object, "5m", "interval")
	}

	// map to HelmRepository
	repo := &unstructured.Unstructured{}
	repo.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "source.toolkit.fluxcd.io",
		Version: "v1beta1",
		Kind:    "HelmRepository",
	})
	unstructured.SetNestedMap(repo.Object, repoSpec.Object, "spec")

	return repo, nil
}

// generateHelmRepoName generates name in format: <namespace>-<releaseName>-helmrepo
func generateHelmRepoName(releaseName, ns string) string {
	return fmt.Sprintf("%s-%s-helmrepo", ns, releaseName)
}
