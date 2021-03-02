package helm

import (
	// "encoding/json"
	// "fmt"
	"testing"
	// "k8s.io/apimachinery/pkg/runtime"
)

func TestGenerateHelmRelease(t *testing.T) {
	//     raw := `
	// helm: |
	//   release:
	//     chart:
	//       spec:
	//         chart: "podinfo"
	//         version: "3.2.0"
	//     values:
	//       replicaCount: 3
	//   repository:
	//     url: "https://stefanprodan.github.io/podinfo"
	// `
	//     valuesRaw := `
	// key1: v1
	// key2:
	//   key3:v3
	// `
	//
	//     helmRls, helmRepo, err := NewGenerateHelmReleaseAndHelmRepo(&runtime.RawExtension{Raw: []byte(raw)}, "svcName", "appName", "ns-test", &runtime.RawExtension{Raw: []byte(valuesRaw)})
	//
	//     d, _ := json.Marshal(helmRls)
	//     fmt.Printf("%s \n", d)
	//
	//     d2, _ := json.Marshal(helmRepo)
	//     fmt.Printf("%s \n", d2)
	//
	//     fmt.Printf("%#v", err)
}
