package traitdefinition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"github.com/pkg/errors"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/pkg/oam/discoverymapper"
)

const (
	errValidateDefRef = "error occurs when validating definition reference"

	failInfoDefRefOmitted = "if definition reference is omitted, patch or output with GVK is required"
	failInfoGVKRequired   = "if definition reference is omitted, output must have GVK"
)

const (
	apiVersionFieldName = "apiVersion"
	kindFieldName       = "kind"
	outputLabel         = "output"
	outputsLabel        = "outputs"
	patchLabel          = "patch"
)

var traitDefGVR = v1alpha2.SchemeGroupVersion.WithResource("traitdefinitions")

// ValidatingHandler handles validation of trait definition
type ValidatingHandler struct {
	Client client.Client
	Mapper discoverymapper.DiscoveryMapper

	// Decoder decodes object
	Decoder *admission.Decoder
	// Validators validate objects
	Validators []TraitDefValidator
}

// TraitDefValidator validate trait definition
type TraitDefValidator interface {
	Validate(context.Context, v1alpha2.TraitDefinition) error
}

// TraitDefValidatorFn implements TraitDefValidator
type TraitDefValidatorFn func(context.Context, v1alpha2.TraitDefinition) error

// Validate implements TraitDefValidator method
func (fn TraitDefValidatorFn) Validate(ctx context.Context, td v1alpha2.TraitDefinition) error {
	return fn(ctx, td)
}

var _ admission.Handler = &ValidatingHandler{}

// Handle validate trait definition
func (h *ValidatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	obj := &v1alpha2.TraitDefinition{}
	if req.Resource.String() != traitDefGVR.String() {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("expect resource to be %s", traitDefGVR))
	}

	if req.Operation == admissionv1beta1.Create || req.Operation == admissionv1beta1.Update {
		err := h.Decoder.Decode(req, obj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		klog.Info("validating ", " name: ", obj.Name, " operation: ", string(req.Operation))
		for _, validator := range h.Validators {
			if err := validator.Validate(ctx, *obj); err != nil {
				klog.Info("validation failed ", " name: ", obj.Name, " errMsgi: ", err.Error())
				return admission.Denied(err.Error())
			}
		}
		klog.Info("validation passed ", " name: ", obj.Name, " operation: ", string(req.Operation))
	}
	return admission.ValidationResponse(true, "")
}

var _ inject.Client = &ValidatingHandler{}

// InjectClient injects the client into the ValidatingHandler
func (h *ValidatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

var _ admission.DecoderInjector = &ValidatingHandler{}

// InjectDecoder injects the decoder into the ValidatingHandler
func (h *ValidatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}

// RegisterValidatingHandler will register TraitDefinition validation to webhook
func RegisterValidatingHandler(mgr manager.Manager) error {
	server := mgr.GetWebhookServer()
	mapper, err := discoverymapper.New(mgr.GetConfig())
	if err != nil {
		return err
	}
	server.Register("/validating-core-oam-dev-v1alpha2-traitdefinitions", &webhook.Admission{Handler: &ValidatingHandler{
		Mapper: mapper,
		Validators: []TraitDefValidator{
			TraitDefValidatorFn(ValidateDefinitionReference),
			// add more validators here
		},
	}})
	return nil
}

// ValidateDefinitionReference validates whether the trait definition is valid if
// its `.spec.reference` field is unset.
// It's valid if
// it has at least one output, and all outputs must have GVK
// or it has no output but has a patch
// or it has a patch and outputs, and all outputs must have GVK
func ValidateDefinitionReference(_ context.Context, td v1alpha2.TraitDefinition) error {
	if td.Spec.Reference.Name != "" {
		return nil
	}

	if td.Spec.Extension == nil || len(td.Spec.Extension.Raw) < 1 {
		return errors.New(failInfoDefRefOmitted)
	}

	tmp := map[string]interface{}{}
	if err := json.Unmarshal(td.Spec.Extension.Raw, &tmp); err != nil {
		return errors.Wrap(err, errValidateDefRef)
	}
	template, ok := tmp["template"]
	if !ok {
		return errors.New(failInfoDefRefOmitted)
	}
	bi := build.NewContext().NewInstance("", nil)
	if err := bi.AddFile("-", fmt.Sprint(template)); err != nil {
		return errors.Wrap(err, errValidateDefRef)
	}
	insts := cue.Build([]*build.Instance{bi})
	for _, inst := range insts {
		if err := inst.Value().Err(); err != nil {
			return errors.Wrap(err, errValidateDefRef)
		}

		hasOutput, pass, err := validateOutputsWithGVK(inst)
		if err != nil {
			return errors.Wrap(err, errValidateDefRef)
		}
		if !pass {
			return errors.New(failInfoGVKRequired)
		}

		// if definitionRef is missing and no output, a patch is required
		if !hasOutput {
			patch := inst.Lookup(patchLabel)
			if patch.Exists() {
				continue
			}
			return errors.New(failInfoDefRefOmitted)
		}
	}
	return nil
}

// validateOutputsWithGVK validates whether any output exists in the instance.
// If exists, further validate whether all output have GVK.
func validateOutputsWithGVK(inst *cue.Instance) (hasOutput bool, validatePass bool, err error) {
	hasOutput = false
	// validate outputs containing multiple output
	outputs := inst.Lookup(outputsLabel)
	st, err := outputs.Struct()
	if err == nil {
		for i := 0; i < st.Len(); i++ {
			f := st.Field(i)
			if f.IsDefinition || f.IsHidden || f.IsOptional {
				continue
			}
			hasOutput = true
			s, innerErr := f.Value.Struct()
			if innerErr != nil {
				return hasOutput, false, innerErr
			}
			// TODO(roywang) how to validate structs in for/if comprehension?
			// if definitionRef is missing, each trait in outputs must have GVK
			if !fieldExistInStruct(s, apiVersionFieldName) ||
				!fieldExistInStruct(s, kindFieldName) {
				return hasOutput, false, nil
			}
		}
	}
	// validate single output
	output := inst.Lookup(outputLabel)
	if output.Exists() {
		hasOutput = true
		oStruct, err := output.Struct()
		if err != nil {
			return hasOutput, false, err
		}
		// if definitionRef is missing, output must have GVK
		if !fieldExistInStruct(oStruct, apiVersionFieldName) ||
			!fieldExistInStruct(oStruct, kindFieldName) {
			return hasOutput, false, nil
		}
	}
	return hasOutput, true, nil
}

// fieldExistInStruct validates the given string field exists in the struct
// and value is not empty
func fieldExistInStruct(o *cue.Struct, fieldName string) bool {
	f, err := o.FieldByName(fieldName, false)
	if err != nil {
		return false
	}
	v, err := f.Value.String()
	if err != nil {
		return false
	}
	if len(v) < 1 {
		return false
	}
	return true
}
