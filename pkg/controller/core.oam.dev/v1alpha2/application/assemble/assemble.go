/*
Copyright 2021 The KubeVela Authors.

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

package assemble

import (
	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplane/crossplane-runtime/pkg/fieldpath"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	ctrlutil "github.com/oam-dev/kubevela/pkg/controller/utils"
	"github.com/oam-dev/kubevela/pkg/oam"
	"github.com/oam-dev/kubevela/pkg/oam/util"
)

// NewAssembleOptions create a Options
func NewAssembleOptions(appRevision *v1beta1.ApplicationRevision) *Options {
	return &Options{AppRevision: appRevision}
}

// Options contains configuration to assemble resources recorded in the ApplicationRevision.
// 'Assemble' means completing workload/trait resources and get them ready to emit into K8s
// cluster.
type Options struct {
	AppRevision     *v1beta1.ApplicationRevision
	WorkloadOptions []WorkloadOption

	AppConfig      *v1alpha2.ApplicationConfiguration
	Comps          []*v1alpha2.Component
	AppName        string
	AppNamespace   string
	AppLables      map[string]string
	AppAnnotations map[string]string
	AppOwnerRef    *metav1.OwnerReference

	// map key is component name
	AssembledWorkloads map[string]*unstructured.Unstructured
	AssembledTraits    map[string][]*unstructured.Unstructured
	ReferencedScopes   map[string][]runtimev1alpha1.TypedReference
}

// WorkloadOption will be applied to each workloads AFTER it has been assembled by generic rules shown below:
// 1) use component name as workload name
// 2) use application namespace as workload namespace if unspecified
// 3) set application as workload's owner
// 4) pass all application's lables and annotations to workload's
// Component and ComponentDefinition are enough for caller to manipulate workloads.
// Caller can use below lables of workload to get more information:
// - oam.LabelAppName
// - oam.LabelAppRevision
// - oam.LabelAppRevisionHash
// - oam.LabelAppComponent
// - oam.LabelAppComponentRevision
type WorkloadOption interface {
	ApplyToWorkload(workload *unstructured.Unstructured, comp *v1alpha2.Component, compDefinition *v1beta1.ComponentDefinition) error
}

// WithWorkloadOption add a WorkloadOption
func (o *Options) WithWorkloadOption(wo WorkloadOption) *Options {
	if o.WorkloadOptions == nil {
		o.WorkloadOptions = make([]WorkloadOption, 0)
	}
	o.WorkloadOptions = append(o.WorkloadOptions, wo)
	return o
}

// Assemble an application's manifests including workloads and traits according to a specific application revision
func (o *Options) Assemble() (map[string]*unstructured.Unstructured, map[string][]*unstructured.Unstructured, map[string][]runtimev1alpha1.TypedReference, error) {
	o.complete()
	if err := o.validate(); err != nil {
		return nil, nil, nil, errors.WithMessagef(err, "cannot assemble manifests of application %q", o.AppName)
	}
	if err := o.assemble(); err != nil {
		return nil, nil, nil, errors.WithMessagef(err, "cannot assemble manifests of application %q", o.AppName)
	}
	return o.AssembledWorkloads, o.AssembledTraits, o.ReferencedScopes, nil
}

func (o *Options) assemble() error {
	for _, acc := range o.AppConfig.Spec.Components {
		compRevisionName := acc.RevisionName
		compName := ctrlutil.ExtractComponentName(compRevisionName)
		commonLables := o.generateCommonLables(compName, compRevisionName)
		var workloadRef runtimev1alpha1.TypedReference
		for _, comp := range o.Comps {
			if comp.Name == compName {
				wl, err := util.RawExtension2Unstructured(&comp.Spec.Workload)
				if err != nil {
					return errors.WithMessagef(err, "cannot convert raw workload in component %q", compName)
				}
				o.setWorkloadName(wl, compName)
				o.setWorkloadLables(wl, commonLables)
				o.setAnnotations(wl)
				o.setNamespace(wl)
				o.setOwnerReference(wl)

				workloadType := wl.GetLabels()[oam.WorkloadTypeLabel]
				compDefinition := o.AppRevision.Spec.ComponentDefinitions[workloadType]
				for _, opt := range o.WorkloadOptions {
					if err := opt.ApplyToWorkload(wl, comp.DeepCopy(), compDefinition.DeepCopy()); err != nil {
						return errors.Wrapf(err, "cannot apply workload option for component %q", compName)
					}
				}
				o.AssembledWorkloads[compName] = wl
				workloadRef = runtimev1alpha1.TypedReference{
					APIVersion: wl.GetAPIVersion(),
					Kind:       wl.GetKind(),
					Name:       wl.GetName(),
				}
				break
			}
		}

		o.AssembledTraits[compName] = make([]*unstructured.Unstructured, len(acc.Traits))
		for i, compTrait := range acc.Traits {
			trait, err := util.RawExtension2Unstructured(&compTrait.Trait)
			if err != nil {
				return errors.WithMessagef(err, "cannot convert raw trait in component %q", compName)
			}
			traitType := trait.GetLabels()[oam.TraitTypeLabel]
			o.setTraitName(trait, compName, traitType, compTrait.DeepCopy())
			o.setTraitLables(trait, commonLables)
			o.setAnnotations(trait)
			o.setNamespace(trait)
			o.setOwnerReference(trait)
			if err := o.setWorkloadRefToTrait(workloadRef, trait); err != nil {
				return errors.WithMessagef(err, "cannot set workload reference to trait %q", trait.GetName())
			}
			o.AssembledTraits[compName][i] = trait
		}

		o.ReferencedScopes[compName] = make([]runtimev1alpha1.TypedReference, len(acc.Scopes))
		for i, scope := range acc.Scopes {
			o.ReferencedScopes[compName][i] = scope.ScopeReference
		}
	}
	return nil
}

func (o *Options) complete() {
	// safe to skip error-check
	o.AppConfig, _ = convertRawExtention2AppConfig(o.AppRevision.Spec.ApplicationConfiguration)
	o.Comps = make([]*v1alpha2.Component, len(o.AppRevision.Spec.Components))
	for i, rawComp := range o.AppRevision.Spec.Components {
		// safe to skip error-check
		comp, _ := convertRawExtention2Component(rawComp.Raw)
		o.Comps[i] = comp
	}
	// Application entity in the ApplicationRevision has no metadata,
	// so we have to get below information from AppConfig.
	// Up-stream process must set these to AppConfig.
	o.AppName = o.AppConfig.GetName()
	o.AppNamespace = o.AppConfig.GetNamespace()
	o.AppLables = o.AppConfig.GetLabels()
	o.AppAnnotations = o.AppConfig.GetAnnotations()
	o.AppOwnerRef = metav1.GetControllerOf(o.AppConfig)

	o.AssembledWorkloads = make(map[string]*unstructured.Unstructured)
	o.AssembledTraits = make(map[string][]*unstructured.Unstructured)
	o.ReferencedScopes = make(map[string][]runtimev1alpha1.TypedReference)
}

// AssembleOptions is highly coulped with AppRevision, should check the AppRevision provides all info
// required by AssembleOptions
func (o *Options) validate() error {
	if o.AppOwnerRef == nil {
		return errors.New("AppRevision must have an Application as owner")
	}
	if len(o.AppRevision.Labels[oam.LabelAppRevisionHash]) == 0 {
		return errors.New("AppRevision must have revision hash recorded in the lable")
	}
	return nil
}

func (o *Options) setWorkloadName(wl *unstructured.Unstructured, compName string) {
	// use component name as workload name
	// override the name set in render phase if exist
	wl.SetName(compName)
}

func (o *Options) setTraitName(trait *unstructured.Unstructured, compName, traitType string, compTrait *v1alpha2.ComponentTrait) {
	// NOTE Comparing to AppConfig, Assemble can not use existing name recorded in AppConifg's status
	// only set generated name when name is unspecified
	// it's by design to set arbitrary name in render phase
	if len(trait.GetName()) == 0 {
		traitName := util.GenTraitName(compName, compTrait, traitType)
		trait.SetName(traitName)
	}
}

// workload and trait in the same component both will have these lables
func (o *Options) generateCommonLables(compName, compRevisionName string) map[string]string {
	lables := map[string]string{
		oam.LabelAppName:              o.AppName,
		oam.LabelAppRevision:          o.AppRevision.Name,
		oam.LabelAppRevisionHash:      o.AppRevision.Labels[oam.LabelAppRevisionHash],
		oam.LabelAppComponent:         compName,
		oam.LabelAppComponentRevision: compRevisionName,
	}
	// pass application's all labels to workload/trait
	return util.MergeMapOverrideWithDst(lables, o.AppLables)
}

func (o *Options) setWorkloadLables(wl *unstructured.Unstructured, commonLables map[string]string) {
	// add more workload-specific labels here
	util.AddLabels(wl, map[string]string{oam.LabelOAMResourceType: oam.ResourceTypeWorkload})
	util.AddLabels(wl, commonLables)

	/* NOTE a workload has these possible labels
	   app.oam.dev/app-revision-hash: ce053923e2fb403f
	   app.oam.dev/appRevision: myapp-v2
	   app.oam.dev/component: mycomp
	   app.oam.dev/name: myapp
	   app.oam.dev/resourceType: WORKLOAD
	   app.oam.dev/revision: mycomp-v2
	   workload.oam.dev/type: kube-worker
	*/
}

func (o *Options) setTraitLables(trait *unstructured.Unstructured, commonLables map[string]string) {
	// add more trait-specific labels here
	util.AddLabels(trait, map[string]string{oam.LabelOAMResourceType: oam.ResourceTypeTrait})
	util.AddLabels(trait, commonLables)

	/* NOTE a trait has these possible labels
	   app.oam.dev/app-revision-hash: ce053923e2fb403f
	   app.oam.dev/appRevision: myapp-v2
	   app.oam.dev/component: mycomp
	   app.oam.dev/name: myapp
	   app.oam.dev/resourceType: TRAIT
	   app.oam.dev/revision: mycomp-v2
	   trait.oam.dev/resource: service
	   trait.oam.dev/type: ingress // already added in render phase
	*/
}

// workload and trait both have these annotations
func (o *Options) setAnnotations(obj *unstructured.Unstructured) {
	// pass application's all annotations
	util.AddAnnotations(obj, o.AppAnnotations)
	// remove useless annotations for workload/trait
	util.RemoveAnnotations(obj, []string{
		oam.AnnotationAppRollout,
		oam.AnnotationRollingComponent,
		oam.AnnotationInplaceUpgrade,
	})
}

func (o *Options) setNamespace(obj *unstructured.Unstructured) {
	// only set app's namespace when namespace is unspecified
	// it's by design to set arbitrary namespace in render phase
	if len(obj.GetNamespace()) == 0 {
		obj.SetNamespace(o.AppNamespace)
	}
}

func (o *Options) setOwnerReference(obj *unstructured.Unstructured) {
	obj.SetOwnerReferences([]metav1.OwnerReference{*o.AppOwnerRef})
}

func (o *Options) setWorkloadRefToTrait(wlRef runtimev1alpha1.TypedReference, trait *unstructured.Unstructured) error {
	traitType := trait.GetLabels()[oam.TraitTypeLabel]
	traitDef := o.AppRevision.Spec.TraitDefinitions[traitType]
	workloadRefPath := traitDef.Spec.WorkloadRefPath
	// only add workload reference to the trait if it asks for it
	if len(workloadRefPath) != 0 {
		if err := fieldpath.Pave(trait.UnstructuredContent()).SetValue(workloadRefPath, wlRef); err != nil {
			return err
		}
	}
	return nil
}
