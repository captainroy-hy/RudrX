package commands

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/crossplane/oam-kubernetes-runtime/pkg/oam"
	"github.com/oam-dev/kubevela/api/types"
	cmdutil "github.com/oam-dev/kubevela/pkg/commands/util"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest/fake"
	"k8s.io/kubectl/pkg/cmd/portforward"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	"k8s.io/kubectl/pkg/scheme"
)

func TestPortForwardCommand(t *testing.T) {
	fakePod := corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:            "fakePod",
			Namespace:       "default",
			ResourceVersion: "10",
			Labels: map[string]string{
				oam.LabelAppComponent: "fakeComp",
			}},
	}
	tf := cmdtesting.NewTestFactory()
	defer tf.Cleanup()

	codec := scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)
	ns := scheme.Codecs.WithoutConversion()
	tf.Client = &fake.RESTClient{
		VersionedAPIPath:     "/api/v1",
		GroupVersion:         schema.GroupVersion{Group: "", Version: "v1"},
		NegotiatedSerializer: ns,
		Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
			body := cmdtesting.ObjBody(codec, &fakePod)
			return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: body}, nil
		}),
	}
	tf.ClientConfigVal = cmdtesting.DefaultClientConfig()

	io := cmdutil.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	fakeC := types.Args{
		Config: tf.ClientConfigVal,
	}
	cmd := NewPortForwardCommand(fakeC, io)
	cmd.PersistentFlags().StringP("env", "e", "", "")
	fakeClientSet := k8sfake.NewSimpleClientset(&corev1.PodList{
		Items: []corev1.Pod{fakePod},
	})

	o := &VelaPortForwardOptions{
		ioStreams:            io,
		kcPortForwardOptions: &portforward.PortForwardOptions{},
		f:                    tf,
		ClientSet:            fakeClientSet,
	}
	err := o.Init(context.Background(), cmd, []string{"fakeApp", "8081:8080"})
	assert.NoError(t, err)
}
