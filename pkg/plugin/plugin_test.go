package plugin

import (
	"context"
	"encoding/json"

	"os"
	"testing"
	"time"

	"github.com/argoproj-labs/rollouts-plugin-trafficrouter-openshift/pkg/mocks"

	fakeDynClient "k8s.io/client-go/dynamic/fake"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	rolloutsPlugin "github.com/argoproj/argo-rollouts/rollout/trafficrouting/plugin/rpc"

	goPlugin "github.com/hashicorp/go-plugin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var testHandshake = goPlugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_RPC_PLUGIN",
	MagicCookieValue: "trafficrouter",
}

func TestRunSuccessfully(t *testing.T) {
	//utils.InitLogger(slog.LevelDebug)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := runtime.NewScheme()

	dynClient := fakeDynClient.NewSimpleDynamicClient(s, mocks.MakeObjects()...)
	rpcPluginImp := &RpcPlugin{
		IsTest:        true,
		dynamicClient: dynClient,
	}

	// pluginMap is the map of plugins we can dispense.
	var pluginMap = map[string]goPlugin.Plugin{
		"RpcTrafficRouterPlugin": &rolloutsPlugin.RpcTrafficRouterPlugin{Impl: rpcPluginImp},
	}

	ch := make(chan *goPlugin.ReattachConfig, 1)
	closeCh := make(chan struct{})
	go goPlugin.Serve(&goPlugin.ServeConfig{
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Test: &goPlugin.ServeTestConfig{
			Context:          ctx,
			ReattachConfigCh: ch,
			CloseCh:          closeCh,
		},
	})

	// We should get a config
	var config *goPlugin.ReattachConfig
	select {
	case config = <-ch:
	case <-time.After(2000 * time.Millisecond):
		t.Fatal("should've received reattach")
	}
	if config == nil {
		t.Fatal("config should not be nil")
	}

	// Connect!
	c := goPlugin.NewClient(&goPlugin.ClientConfig{
		Cmd:             nil,
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Reattach:        config,
	})
	client, err := c.Client()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Pinging should work
	if err := client.Ping(); err != nil {
		t.Fatalf("should not err: %s", err)
	}

	// Kill which should do nothing
	c.Kill()
	if err := client.Ping(); err != nil {
		t.Fatalf("should not err: %s", err)
	}

	if err := rpcPluginImp.InitPlugin(); err.HasError() {
		t.Fail()
	}

	t.Run("SetWeight", func(t *testing.T) {
		rollout := newRollout(mocks.StableServiceName, mocks.CanaryServiceName, mocks.RouteName)
		desiredWeight := int32(30)

		if err := rpcPluginImp.SetWeight(rollout, desiredWeight, []v1alpha1.WeightDestination{}); err.HasError() {
			t.Fail()
		}

		alternateBackends := rpcPluginImp.UpdatedRoute.Spec.AlternateBackends

		if 100-desiredWeight != int32(*alternateBackends[0].Weight) {
			t.Fail()
		}
		if desiredWeight != int32(*alternateBackends[1].Weight) {
			t.Fail()
		}

	})

}

func newRollout(stableSvc, canarySvc, routeName string) *v1alpha1.Rollout {
	contourConfig := OpenshiftTrafficRouting{
		Routes: []string{routeName},
	}
	encodedContourConfig, err := json.Marshal(contourConfig)
	if err != nil {
		//slog.Error("marshal the route's config is failed", slog.Any("err", err))
		os.Exit(1)
	}

	return &v1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout",
			Namespace: "default",
		},
		Spec: v1alpha1.RolloutSpec{
			Strategy: v1alpha1.RolloutStrategy{
				Canary: &v1alpha1.CanaryStrategy{
					StableService: stableSvc,
					CanaryService: canarySvc,
					TrafficRouting: &v1alpha1.RolloutTrafficRouting{
						Plugins: map[string]json.RawMessage{
							"argoproj-labs/openshift": encodedContourConfig,
						},
					},
				},
			},
		},
	}
}
