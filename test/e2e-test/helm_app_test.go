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

package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/common"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/pkg/oam/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test application containing helm module", func() {
	ctx := context.Background()
	var (
		namespace = "helm-test-ns"
		appName   = "test-app"
		compName  = "test-comp"
		cdName    = "webapp-chart"
		wdName    = "webapp-chart-wd"
		tdName    = "virtualgroup"
	)
	var app v1alpha2.Application
	var ns corev1.Namespace

	BeforeEach(func() {
		ns = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Eventually(
			func() error {
				return k8sClient.Delete(ctx, &ns, client.PropagationPolicy(metav1.DeletePropagationForeground))
			},
			time.Second*120, time.Millisecond*500).Should(SatisfyAny(BeNil(), &util.NotFoundMatcher{}))
		By("make sure all the resources are removed")
		objectKey := client.ObjectKey{
			Name: namespace,
		}
		Eventually(
			func() error {
				return k8sClient.Get(ctx, objectKey, &corev1.Namespace{})
			},
			time.Second*120, time.Millisecond*500).Should(&util.NotFoundMatcher{})
		Eventually(
			func() error {
				return k8sClient.Create(ctx, &ns)
			},
			time.Second*3, time.Millisecond*300).Should(SatisfyAny(BeNil(), &util.AlreadyExistMatcher{}))

		cd := v1alpha2.ComponentDefinition{}
		cd.SetName(cdName)
		cd.SetNamespace(namespace)
		cd.Spec.Workload.Definition = common.WorkloadGVK{APIVersion: "apps/v1", Kind: "Deployment"}
		cd.Spec.Schematic = &common.Schematic{
			HELM: &common.Helm{
				Release: util.Object2RawExtension(map[string]interface{}{
					"chart": map[string]interface{}{
						"spec": map[string]interface{}{
							"chart":   "podinfo",
							"version": "5.1.4",
						},
					},
				}),
				Repository: util.Object2RawExtension(map[string]interface{}{
					"url": "http://oam.dev/catalog/",
				}),
			},
		}
		Expect(k8sClient.Create(ctx, &cd)).Should(Succeed())

		By("Install a patch trait used to test CUE module")
		td := v1alpha2.TraitDefinition{}
		td.SetName(tdName)
		td.SetNamespace(namespace)
		td.Spec.AppliesToWorkloads = []string{"deployments.apps"}
		td.Spec.Schematic = &common.Schematic{
			CUE: &common.CUE{
				Template: `patch: {
      	spec: template: {
      		metadata: labels: {
      			if parameter.type == "namespace" {
      				"app.namespace.virtual.group": parameter.group
      			}
      			if parameter.type == "cluster" {
      				"app.cluster.virtual.group": parameter.group
      			}
      		}
      	}
      }
      parameter: {
      	group: *"default" | string
      	type:  *"namespace" | string
      }`,
			},
		}
		Expect(k8sClient.Create(ctx, &td)).Should(Succeed())

		By("Add 'deployments.apps' to scaler's appliesToWorkloads")
		scalerTd := v1alpha2.TraitDefinition{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "scaler", Namespace: "vela-system"}, &scalerTd)).Should(Succeed())
		scalerTd.Spec.AppliesToWorkloads = []string{"deployments.apps", "webservice", "worker"}
		scalerTd.SetResourceVersion("")
		Expect(k8sClient.Patch(ctx, &scalerTd, client.Merge)).Should(Succeed())
	})

	AfterEach(func() {
		By("Clean up resources after a test")
		k8sClient.DeleteAllOf(ctx, &v1alpha2.Application{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1alpha2.ComponentDefinition{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1alpha2.WorkloadDefinition{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1alpha2.TraitDefinition{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(ctx, &ns, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(15 * time.Second)

		By("Remove 'deployments.apps' from scaler's appliesToWorkloads")
		scalerTd := v1alpha2.TraitDefinition{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "scaler", Namespace: "vela-system"}, &scalerTd)).Should(Succeed())
		scalerTd.Spec.AppliesToWorkloads = []string{"webservice", "worker"}
		scalerTd.SetResourceVersion("")
		Expect(k8sClient.Patch(ctx, &scalerTd, client.Merge)).Should(Succeed())
	})

	It("Test deploy an application containing helm module", func() {
		app = v1alpha2.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appName,
				Namespace: namespace,
			},
			Spec: v1alpha2.ApplicationSpec{
				Components: []v1alpha2.ApplicationComponent{
					{
						Name:         compName,
						WorkloadType: cdName,
						Settings: util.Object2RawExtension(map[string]interface{}{
							"image": map[string]interface{}{
								"tag": "5.1.2",
							},
						}),
						Traits: []v1alpha2.ApplicationTrait{
							{
								Name: "scaler",
								Properties: util.Object2RawExtension(map[string]interface{}{
									"replicas": 2,
								}),
							},
							{
								Name: tdName,
								Properties: util.Object2RawExtension(map[string]interface{}{
									"group": "my-group",
									"type":  "cluster",
								}),
							},
						},
					},
				},
			},
		}
		By("Create application")
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		ac := &v1alpha2.ApplicationContext{}
		acName := appName
		By("Verify the ApplicationContext is created successfully")
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: acName, Namespace: namespace}, ac)
		}, 30*time.Second, time.Second).Should(Succeed())

		By("Verify the workload(deployment) is created successfully by Helm")
		deploy := &appsv1.Deployment{}
		deployName := fmt.Sprintf("%s-%s-podinfo", appName, compName)
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: deployName, Namespace: namespace}, deploy)
		}, 120*time.Second, 5*time.Second).Should(Succeed())

		By("Verify two traits are applied to the workload")
		Eventually(func() bool {
			requestReconcileNow(ctx, ac)
			deploy := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: deployName, Namespace: namespace}, deploy); err != nil {
				return false
			}
			By("Verify patch trait is applied")
			templateLabels := deploy.Spec.Template.Labels
			if templateLabels["app.cluster.virtual.group"] != "my-group" {
				return false
			}
			By("Verify scaler trait is applied")
			if *deploy.Spec.Replicas != 2 {
				return false
			}
			By("Verify application's settings override chart default values")
			// the default value of 'image.tag' is 5.1.4 in the chart, but settings reset it to 5.1.2
			return strings.HasSuffix(deploy.Spec.Template.Spec.Containers[0].Image, "5.1.2")
			// it takes pretty long time to fetch chart and install the Helm release
		}, 120*time.Second, 10*time.Second).Should(BeTrue())

		By("Update the application")
		app = v1alpha2.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appName,
				Namespace: namespace,
			},
			Spec: v1alpha2.ApplicationSpec{
				Components: []v1alpha2.ApplicationComponent{
					{
						Name:         compName,
						WorkloadType: cdName,
						Settings: util.Object2RawExtension(map[string]interface{}{
							"image": map[string]interface{}{
								"tag": "5.1.3", // change 5.1.4 => 5.1.3
							},
						}),
						Traits: []v1alpha2.ApplicationTrait{
							{
								Name: "scaler",
								Properties: util.Object2RawExtension(map[string]interface{}{
									"replicas": 3, // change 2 => 3
								}),
							},
							{
								Name: tdName,
								Properties: util.Object2RawExtension(map[string]interface{}{
									"group": "my-group-0", // change my-group => my-group-0
									"type":  "cluster",
								}),
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Patch(ctx, &app, client.Merge)).Should(Succeed())

		By("Verify the ApplicationContext is updated")
		deploy = &appsv1.Deployment{}
		Eventually(func() bool {
			ac = &v1alpha2.ApplicationContext{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: acName, Namespace: namespace}, ac); err != nil {
				return false
			}
			return ac.GetGeneration() == 2
		}, 15*time.Second, 3*time.Second).Should(BeTrue())

		By("Verify the changes are applied to the workload")
		Eventually(func() bool {
			requestReconcileNow(ctx, ac)
			deploy := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: deployName, Namespace: namespace}, deploy); err != nil {
				return false
			}
			By("Verify new patch trait is applied")
			templateLabels := deploy.Spec.Template.Labels
			if templateLabels["app.cluster.virtual.group"] != "my-group-0" {
				return false
			}
			By("Verify new scaler trait is applied")
			// TODO(roywang) how to enforce scaler controller reconcile
			// immediately? e2e test cannot wait 5min for reconciliation.
			if *deploy.Spec.Replicas == 2 {
				return false
			}
			By("Verify new application's settings override chart default values")
			return strings.HasSuffix(deploy.Spec.Template.Spec.Containers[0].Image, "5.1.3")
		}, 60*time.Second, 10*time.Second).Should(BeTrue())
	})

	It("Test deploy an application containing helm module defined by workloadDefinition", func() {

		workloaddef := v1alpha2.WorkloadDefinition{}
		workloaddef.SetName(wdName)
		workloaddef.SetNamespace(namespace)
		workloaddef.Spec.Reference = common.DefinitionReference{Name: "deployments.apps", Version: "v1"}
		workloaddef.Spec.Schematic = &common.Schematic{
			HELM: &common.Helm{
				Release: util.Object2RawExtension(map[string]interface{}{
					"chart": map[string]interface{}{
						"spec": map[string]interface{}{
							"chart":   "podinfo",
							"version": "5.1.4",
						},
					},
				}),
				Repository: util.Object2RawExtension(map[string]interface{}{
					"url": "http://oam.dev/catalog/",
				}),
			},
		}
		By("register workloadDefinition")
		Expect(k8sClient.Create(ctx, &workloaddef)).Should(Succeed())

		appTestName := "test-app-refer-to-workloaddef"
		appTest := v1alpha2.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appTestName,
				Namespace: namespace,
			},
			Spec: v1alpha2.ApplicationSpec{
				Components: []v1alpha2.ApplicationComponent{
					{
						Name:         compName,
						WorkloadType: wdName,
						Settings: util.Object2RawExtension(map[string]interface{}{
							"image": map[string]interface{}{
								"tag": "5.1.2",
							},
						}),
					},
				},
			},
		}
		By("Create application")
		Expect(k8sClient.Create(ctx, &appTest)).Should(Succeed())

		ac := &v1alpha2.ApplicationContext{}
		acName := appTestName
		By("Verify the AppConfig is created successfully")
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: acName, Namespace: namespace}, ac)
		}, 30*time.Second, time.Second).Should(Succeed())

		By("Verify the workload(deployment) is created successfully by Helm")
		deploy := &appsv1.Deployment{}
		deployName := fmt.Sprintf("%s-%s-podinfo", appTestName, compName)
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: deployName, Namespace: namespace}, deploy)
		}, 240*time.Second, 5*time.Second).Should(Succeed())
	})

	It("Test store JSON schema of Helm Chart in ConfigMap", func() {
		By("Get the ConfigMap")
		cmName := fmt.Sprintf("schema-%s", cdName)
		Eventually(func() error {
			cm := &corev1.ConfigMap{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: cmName, Namespace: namespace}, cm); err != nil {
				return err
			}
			if cm.Data["openapi-v3-json-schema"] == "" {
				return errors.New("json schema is not found in the ConfigMap")
			}
			return nil
		}, 60*time.Second, 5*time.Second).Should(Succeed())
	})
})
