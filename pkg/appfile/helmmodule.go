package appfile

import (
	"encoding/json"

	"github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/pkg/appfile/helm"
	"github.com/oam-dev/kubevela/pkg/oam"
	"github.com/oam-dev/kubevela/pkg/oam/discoverymapper"
	"github.com/oam-dev/kubevela/pkg/oam/util"
)

func generateComponentFromHelmModule(c client.Client, dm discoverymapper.DiscoveryMapper, wl *Workload, appName string, ns string) (*v1alpha2.Component, *v1alpha2.ApplicationConfigurationComponent, error) {
	comp := &v1alpha2.Component{}
	acComp := &v1alpha2.ApplicationConfigurationComponent{}

	rls, repo, err := helm.GenerateHelmReleaseAndHelmRepo(wl.Template, wl.Name, appName, ns, wl.Params)
	if err != nil {
		return nil, nil, err
	}

	targetWokrloadGVK, err := util.GetGVKFromDefinition(dm, wl.Reference)
	if err != nil {
		return nil, nil, err
	}
	targetWorkload := unstructured.Unstructured{}
	targetWorkload.SetGroupVersionKind(targetWokrloadGVK)

	bts, _ := json.Marshal(targetWorkload.Object)
	comp.Spec.Workload = runtime.RawExtension{Raw: bts}
	rlsBytes, _ := json.Marshal(rls.Object)
	repoBytes, _ := json.Marshal(repo.Object)

	comp.Spec.HelmModule = &v1alpha2.HelmModuleResource{
		HelmRelease:    runtime.RawExtension{Raw: rlsBytes},
		HelmRepository: runtime.RawExtension{Raw: repoBytes},
	}

	comp.Name = wl.Name
	comp.Namespace = ns
	if comp.Labels == nil {
		comp.Labels = map[string]string{}
	}
	comp.Labels[oam.LabelAppName] = appName
	comp.SetGroupVersionKind(v1alpha2.ComponentGroupVersionKind)

	acComp.ComponentName = comp.Name

	// TODO currently use a fake CUE template to make it workable to generate traits
	// we should replace it with a real CUE template representing the workload from Helm module
	// then trait's CUE module can also work on workload from Helm module
	wl.Template = `
output: {}
	`
	pCtx, err := PrepareProcessContext(c, wl, appName, ns)
	if err != nil {
		return nil, nil, err
	}
	for _, tr := range wl.Traits {
		if err := tr.EvalContext(pCtx); err != nil {
			return nil, nil, errors.Wrapf(err, "evaluate template trait=%s app=%s", tr.Name, wl.Name)
		}
	}

	_, assists := pCtx.Output()
	for _, assist := range assists {
		tr, err := assist.Ins.Unstructured()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "evaluate trait=%s template for component=%s app=%s", assist.Name, comp.Name, appName)
		}
		labels := map[string]string{
			oam.TraitTypeLabel:    assist.Type,
			oam.LabelAppName:      appName,
			oam.LabelAppComponent: comp.Name,
		}
		if assist.Name != "" {
			labels[oam.TraitResource] = assist.Name
		}
		util.AddLabels(tr, labels)
		acComp.Traits = append(acComp.Traits, v1alpha2.ComponentTrait{
			// we need to marshal the trait to byte array before sending them to the k8s
			Trait: util.Object2RawExtension(tr),
		})
	}

	for _, sc := range wl.Scopes {
		acComp.Scopes = append(acComp.Scopes, v1alpha2.ComponentScope{ScopeReference: v1alpha1.TypedReference{
			APIVersion: sc.GVK.GroupVersion().String(),
			Kind:       sc.GVK.Kind,
			Name:       sc.Name,
		}})
	}
	return comp, acComp, nil
}
