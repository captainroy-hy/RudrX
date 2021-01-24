package traitdefinition

import (
	"context"
	"fmt"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/oam-dev/kubevela/pkg/oam/util"
)

func TestValidateListComprehension(t *testing.T) {

}

func TestValidateDefinitionReference(t *testing.T) {
	cases := map[string]struct {
		reason   string
		template string
		want     error
	}{
		"HavePatch": {
			reason: "No error should be returned if have patch and no output",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }`,
			want: nil,
		},
		"HavePatch_OutputHasNoGVK": {
			reason: "An error should be returned if output has no GVK",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }
      output: {
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"HavePatch_OutputHasInvalidGVK_1": {
			reason: "An error should be returned if output has no GVK",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }
      output: {
      	apiVersion: ""
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"HavePatch_OutputHasInvalidGVK_2": {
			reason: "An error should be returned if output has no GVK",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }
      output: {
      	apiVersion: 123 
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"HavePatch_OutputHasGVK": {
			reason: "No error should be returned if have a patch and output has GVK",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }
      output: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: nil,
		},
		"HavePatch_OutputHasGVK_OutputsHaveGVK": {
			reason: "No error should be returned if have a patch and all outputs has GVK",
			template: `
    template: |-
      patch: {
       spec: replicas: parameter.replicas
      }
      output: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: nil,
		},
		"NoPatch_OutputHasGVK": {
			reason: "No error should be returned if output has GVK",
			template: `
    template: |-
      output: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: nil,
		},
		"NoPatch_OutputHasNoGVk": {
			reason: "An error should be returned if output has no GVK",
			template: `
    template: |-
      output: {
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"NoPatch_AllOutputsHaveGVK": {
			reason: "No error should be returned if each outputs has GVK",
			template: `
    template: |-
      outputs: faketrait1: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait2: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait3: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: nil,
		},
		"NoPatch_PartialOutputsHaveGVK": {
			reason: "No error should be returned if each outputs has GVK",
			template: `
    template: |-
      outputs: faketrait1: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait2: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait3: {
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"NoPatch_OutputHasGVK_OutputsHaveNoGVK": {
			reason: "An error should be returned if any of outputs has no GVK",
			template: `
    template: |-
      output: {
      	apiVersion: "core.oam.dev/v1alpha2"
      	kind:       "ManualScalerTrait"
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }
      outputs: faketrait: {
      	spec: {
      	    replicaCount: parameter.replicas
      	}
      }`,
			want: errors.New(failInfoGVKRequired),
		},
		"NoPatch_NoOutput": {
			reason: "An error should be returned if definitionRef is missing",
			template: `
    template: |-
      fakefield: fakefieldvalue`,
			want: errors.New(failInfoDefRefOmitted),
		},
		"NoTemplate": {
			reason: "An error should be returned if definitionRef is missing",
			template: `
    notemplate: |-
      fakefield: fakefieldvalue`,
			want: errors.New(failInfoDefRefOmitted),
		},
	}

	for caseName, tc := range cases {
		t.Run(caseName, func(t *testing.T) {
			tdStr := traitDefStringWithTemplate(tc.template)
			td, err := util.UnMarshalStringToTraitDefinition(tdStr)
			if err != nil {
				t.Fatal("error occurs in generating TraitDefinition string", err.Error())
			}
			err = ValidateDefinitionReference(context.Background(), *td)
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nValidateDefinitionReference: -want , +got \n%s\n", tc.reason, diff)
			}
		})
	}

	t.Run("NoExtension", func(t *testing.T) {
		tdStr := traitDefStringWithTemplate("")
		td, err := util.UnMarshalStringToTraitDefinition(tdStr)
		if err != nil {
			t.Fatal("error occurs in generating TraitDefinition string", err.Error())
		}
		td.Spec.Extension = nil
		wantErr := errors.New(failInfoDefRefOmitted)
		err = ValidateDefinitionReference(context.Background(), *td)
		if diff := cmp.Diff(wantErr, err, test.EquateErrors()); diff != "" {
			t.Errorf("\n%s\nValidateDefinitionReference: -want , +got \n%s\n", "An error should be returned", diff)
		}
	})

}

func traitDefStringWithTemplate(t string) string {
	return fmt.Sprintf(`
apiVersion: core.oam.dev/v1alpha2
kind: TraitDefinition
metadata:
  name: scaler
spec:
  appliesToWorkloads:
    - webservice
    - worker
  extension:
%s`, t)
}
