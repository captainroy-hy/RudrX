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

package applicationrollout

import (
	"context"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/pkg/controller/core.oam.dev/v1alpha2/application/assemble"
	"github.com/oam-dev/kubevela/pkg/controller/core.oam.dev/v1alpha2/application/dispatch"
	"github.com/oam-dev/kubevela/pkg/controller/utils"
	oamutil "github.com/oam-dev/kubevela/pkg/oam/util"
)

func (r *Reconciler) getAppRevision(ctx context.Context, revName string) (*v1beta1.ApplicationRevision, error) {
	var appRevision v1beta1.ApplicationRevision
	ns := oamutil.GetDefinitionNamespaceWithCtx(ctx)
	if err := r.Get(ctx, ktypes.NamespacedName{Namespace: ns, Name: revName}, &appRevision); err != nil {
		klog.ErrorS(err, "cannot locate application revision", "appRevision", klog.KRef(ns, revName))
		return nil, err
	}
	return &appRevision, nil
}

// emitAppRevisionForRollout play a semilar role as application controller which emits an application
// revision's resources into cluster and make the workload prepared for rollout
func (r *Reconciler) emitAppRevisionForRollout(ctx context.Context, curAppRev, previsouAppRev *v1beta1.ApplicationRevision) error {
	m, err := getAssembledManifests(curAppRev, true)
	if err != nil {
		return err
	}
	// currently only support rollout application with one component
	// the 1st item of assembled manifests is workload
	if len(m) == 0 {
		// this is impossible
		return errors.New("assembled manifests is empty")
	}
	workload := m[0].DeepCopy()

	d := dispatch.NewAppManifestsDispatcher(r.Client, curAppRev)
	// If a source revision is given, it will upgrade the owner of source revision's resources to
	// the target revision.This is useful while target and source revision have the same resouces.
	// It allows target revision to 'inherit' existing resources created by source revision instead
	// of deleting old ones and creating again.
	if previsouAppRev != nil {
		sourceRT := &v1beta1.ResourceTracker{}
		sourceRT.SetName(dispatch.ConstructResourceTrackerName(previsouAppRev.Name, previsouAppRev.Namespace))
		d = d.EnableUpgradeAndSkipGC(sourceRT)
	}
	if _, err := d.Dispatch(ctx, m); err != nil {
		return errors.WithMessagef(err, "cannot dispatch resources' manifests of app revision %q", curAppRev.Name)
	}
	// make sure we can get the workload from cluster
	verifyWorkloadExists := func() (bool, error) {
		wl := workload.DeepCopy()
		if err := r.Client.Get(ctx, client.ObjectKey{Name: wl.GetName(), Namespace: wl.GetNamespace()}, wl); err != nil {
			return false, err
		}
		*workload = *wl
		return true, nil
	}
	if err := wait.ExponentialBackoff(utils.DefaultBackoff, verifyWorkloadExists); err != nil {
		return err
	}
	if err := r.disableCtrlOwner(ctx, workload); err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) disableCtrlOwner(ctx context.Context, wl *unstructured.Unstructured) error {
	wlPatch := client.MergeFrom(wl.DeepCopyObject())
	owners := []metav1.OwnerReference{}
	for _, o := range wl.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller {
			// disable existing controller owner
			o.Controller = pointer.BoolPtr(false)
		}
		owners = append(owners, o)
	}
	wl.SetOwnerReferences(owners)
	// patch the Deployment
	if err := r.Client.Patch(ctx, wl, wlPatch); err != nil {
		return err
	}
	return nil
}

func getWorkload(appRev *v1beta1.ApplicationRevision) (*unstructured.Unstructured, error) {
	m, err := getAssembledManifests(appRev, true)
	if err != nil {
		return nil, err
	}
	// currently only support application with one component
	// 1st item of assembled manifests is workload
	if len(m) == 0 {
		// this is impossible
		return nil, errors.New("assembled manifests is empty")
	}
	return m[0], nil
}

func getAssembledManifests(appRev *v1beta1.ApplicationRevision, prepareRollout bool) ([]*unstructured.Unstructured, error) {
	a := assemble.NewAppManifests(appRev).
		WithWorkloadOption(assemble.NameNonInplaceUpgradableWorkload()) // name non-InplaceUpgrade workload
	if prepareRollout {
		a = a.WithWorkloadOption(assemble.PrepareWorkloadForRollout())
	}
	manifests, err := a.AssembledManifests()
	if err != nil {
		return nil, errors.WithMessagef(err, "cannot assemble resources' manifests of app revision %q", appRev.Name)
	}
	return manifests, nil
}
