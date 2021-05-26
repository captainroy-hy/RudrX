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
	"io/ioutil"
	"testing"

	"github.com/ghodss/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/pkg/oam"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAssemble(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Assemble Suite")
}

var _ = Describe("Test Assemble Options", func() {
	It("test assemble", func() {
		var (
			compName  = "test-comp"
			namespace = "default"
		)

		appRev := &v1beta1.ApplicationRevision{}
		b, err := ioutil.ReadFile("./testdata/apprevision.yaml")
		/* appRevision test data is generated based on below application
		apiVersion: core.oam.dev/v1beta1
		kind: Application
		metadata:
		  name: test-assemble
		spec:
		  components:
		    - name: test-comp
		      type: webservice
		      properties:
		        image: crccheck/hello-world
		        port: 8000
		      traits:
		        - type: ingress
		          properties:
		            domain: localhost
		            http:
		              "/": 8000
		*/
		Expect(err).Should(BeNil())
		err = yaml.Unmarshal(b, appRev)
		Expect(err).Should(BeNil())

		ao := NewAssembleOptions(appRev)
		workloads, traits, _, err := ao.Assemble()
		Expect(err).Should(BeNil())

		By("Verify amount of result manifests")
		Expect(len(workloads)).Should(Equal(1))
		Expect(len(traits[compName])).Should(Equal(2))

		By("Verify workload metadata (name, namespace, labels, annotations, ownerRef)")
		wl := workloads[compName]
		Expect(wl.GetName()).Should(Equal(compName))
		Expect(wl.GetNamespace()).Should(Equal(namespace))
		labels := wl.GetLabels()
		lableKeys := make([]string, 0, len(labels))
		for k := range labels {
			lableKeys = append(lableKeys, k)
		}
		Expect(lableKeys).Should(ContainElements(
			oam.LabelAppName,
			oam.LabelAppRevision,
			oam.LabelAppRevisionHash,
			oam.LabelAppComponent,
			oam.LabelAppComponentRevision,
			oam.WorkloadTypeLabel,
			oam.LabelOAMResourceType))
		Expect(len(wl.GetAnnotations())).Should(Equal(1))
		ownerRef := metav1.GetControllerOf(wl)
		Expect(ownerRef.Kind).Should(Equal("Application"))

		By("Verify trait metadata (name, namespace, labels, annotations, ownerRef)")
		trait := traits[compName][0]
		Expect(trait.GetName()).Should(ContainSubstring(compName))
		Expect(trait.GetNamespace()).Should(Equal(namespace))
		labels = trait.GetLabels()
		lableKeys = make([]string, 0, len(labels))
		for k := range labels {
			lableKeys = append(lableKeys, k)
		}
		Expect(lableKeys).Should(ContainElements(
			oam.LabelAppName,
			oam.LabelAppRevision,
			oam.LabelAppRevisionHash,
			oam.LabelAppComponent,
			oam.LabelAppComponentRevision,
			oam.TraitTypeLabel,
			oam.LabelOAMResourceType))
		Expect(len(wl.GetAnnotations())).Should(Equal(1))
		ownerRef = metav1.GetControllerOf(trait)
		Expect(ownerRef.Kind).Should(Equal("Application"))
	})
})
