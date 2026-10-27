package boundary_test

import (
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
	"strings"
	"testing"
)

func TestClusterCacheBoundary(t *testing.T) {
	b := loadLibrary(t, "clusterapi/clustercache.yaml")
	if b.Spec.Upstream != "sigs.k8s.io/cluster-api" || b.Spec.Version != "v1.9.0" || b.Spec.Cluster != "management" {
		t.Fatalf("pin %s@%s in %q", b.Spec.Upstream, b.Spec.Version, b.Spec.Cluster)
	}
	watches, effects := b.Facts("cache")
	for _, tc := range []struct {
		id      string
		cluster ir.ClusterID
	}{
		{id: "effect/readCluster", cluster: "management"},
		{id: "effect/readKubeconfig", cluster: "management"},
		{id: "effect/listNodesWorkloadA", cluster: "workload_a"},
		{id: "effect/listNodesWorkloadB", cluster: "workload_b"},
		{id: "watch/clusters", cluster: "management"},
		{id: "watch/nodesWorkloadA", cluster: "workload_a"},
		{id: "watch/nodesWorkloadB", cluster: "workload_b"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			kind, id, _ := strings.Cut(tc.id, "/")
			found := false
			if kind == "effect" {
				for _, e := range effects {
					if e.ID == id {
						found = e.Cluster == tc.cluster && e.Evidence == ir.Assumed
					}
				}
			}
			for _, w := range watches {
				if kind == "watch" && w.ID == id {
					found = w.Cluster == tc.cluster && w.Evidence == ir.Assumed
				}
			}
			if !found {
				t.Fatalf("%s not found as an assumed fact in cluster %q", tc.id, tc.cluster)
			}
		})
	}
	// The workload clusters are distinct ClusterIDs, and each names its own
	// state in the model.
	workloads := map[ir.ClusterID]bool{}
	for _, e := range effects {
		if e.Cluster != b.Spec.Cluster {
			workloads[e.Cluster] = true
		}
	}
	if len(workloads) != 2 {
		t.Fatalf("workload clusters %v, want two", workloads)
	}
	for c := range workloads {
		for _, suffix := range []string{"_connected", "_cached"} {
			if _, ok := b.Model.State[string(c)+suffix]; !ok {
				t.Errorf("model has no state %s%s", c, suffix)
			}
		}
	}
}

// nodeConsumer is a management-cluster controller that reads workload nodes
// through the cluster cache.
func nodeConsumer() (model.Spec, map[string]composition.Port) {
	spec := model.Spec{Name: "Consumer", State: map[string]model.Variable{
		"nodes":   {Type: "int", Initial: "0"},
		"healthy": {Type: "bool", Initial: "false"},
	}, Actions: []model.Action{{Name: "check", Guard: "nodes > 0 && !healthy", Updates: map[string]expr.Expression{"healthy": "true"}}}}
	return spec, map[string]composition.Port{"nodes": {State: "nodes", Contract: "cluster.x-k8s.io/clustercache", Version: "v1", Direction: "in"}}
}

func TestClusterCacheComposes(t *testing.T) {
	consumer, ports := nodeConsumer()
	for _, tc := range []struct {
		name   string
		links  []composition.Link
		events []string
		err    string
		want   map[string]any
	}{
		{name: "cross-cluster join needs an explicit link", err: "input consumer.nodes requires one provider of cluster.x-k8s.io/clustercache/v1; found [cache.workloadA cache.workloadB]"},
		{name: "consumer reads workload A after sync", links: []composition.Link{{From: "cache.workloadA", To: "consumer.nodes"}}, events: []string{"cache_connect_workload_a", "cache_sync_workload_a", "consumer_check"}, want: map[string]any{"consumer_healthy": true}},
		{name: "workload B does not feed a consumer bound to A", links: []composition.Link{{From: "cache.workloadA", To: "consumer.nodes"}}, events: []string{"cache_connect_workload_b", "cache_sync_workload_b", "consumer_check"}, err: "action consumer_check disabled"},
		{name: "connection loss drops the cache", links: []composition.Link{{From: "cache.workloadA", To: "consumer.nodes"}}, events: []string{"cache_connect_workload_a", "cache_sync_workload_a", "cache_disconnect_workload_a", "consumer_check"}, err: "action consumer_check disabled"},
		{name: "reconnect fills a fresh cache", links: []composition.Link{{From: "cache.workloadA", To: "consumer.nodes"}}, events: []string{"cache_connect_workload_a", "cache_sync_workload_a", "cache_disconnect_workload_a", "cache_connect_workload_a", "cache_sync_workload_a", "consumer_check"}, want: map[string]any{"cache_workload_a_cached": int64(1), "consumer_healthy": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded, err := system{boundaries: map[string]string{"cache": "clusterapi/clustercache.yaml"}, consumer: consumer, ports: ports, links: tc.links}.compose(t)
			var state map[string]any
			if err == nil {
				state, err = run(t, loaded.Result.Model, tc.events...)
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.want {
				if state[k] != v {
					t.Errorf("%s = %v, want %v", k, state[k], v)
				}
			}
		})
	}
}

// TestClusterCacheResourceJoinStaysInCluster shows that a resource join never
// reaches a workload cluster through the cache: the cache reads Nodes there
// but offers no Node port.
func TestClusterCacheResourceJoinStaysInCluster(t *testing.T) {
	consumer, ports := nodeConsumer()
	loaded, err := system{boundaries: map[string]string{"cache": "clusterapi/clustercache.yaml"}, consumer: consumer, ports: ports, links: []composition.Link{{From: "cache.workloadA", To: "consumer.nodes"}}}.compose(t)
	if err != nil {
		t.Fatal(err)
	}
	cache := loaded.Components[0]
	reader := composition.Component{Name: "reader", Ports: map[string]composition.Port{"nodes": {State: "nodes", Contract: composition.ResourceContract(ir.GVK{Version: "v1", Kind: "Node"}), Version: composition.ResourceContractVersion, Direction: "in"}}}
	for _, cluster := range []ir.ClusterID{"management", "workload_a"} {
		t.Run(string(cluster), func(t *testing.T) {
			links, diags, err := composition.JoinByResource([]composition.Component{cache, reader}, map[string]ir.ClusterID{"cache": "management", "reader": cluster}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 0 || len(diags) != 1 || diags[0].Code != composition.DiagUnresolvedProvider || diags[0].Port != "reader.nodes" {
				t.Fatalf("links %v diagnostics %v", links, diags)
			}
		})
	}
}
