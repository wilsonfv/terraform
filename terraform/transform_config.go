package terraform

import (
	"log"
	"sync"

	"github.com/hashicorp/terraform/addrs"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/dag"
)

// ConfigTransformer is a GraphTransformer that adds all the resources
// from the configuration to the graph.
//
// The module used to configure this transformer must be the root module.
//
// Only resources are added to the graph. Variables, outputs, and
// providers must be added via other transforms.
//
// Unlike ConfigTransformerOld, this transformer creates a graph with
// all resources including module resources, rather than creating module
// nodes that are then "flattened".
type ConfigTransformer struct {
	Concrete ConcreteResourceNodeFunc

	// Module is the module to add resources from.
	Config *configs.Config

	// Unique will only add resources that aren't already present in the graph.
	Unique bool

	// Mode will only add resources that match the given mode
	ModeFilter bool
	Mode       addrs.ResourceMode

	l         sync.Mutex
	uniqueMap map[string]struct{}
}

// FIXME: should we have an addr.Module + addr.Resource type?
type configName interface {
	Name() string
}

func (t *ConfigTransformer) Transform(g *Graph) error {
	// Lock since we use some internal state
	t.l.Lock()
	defer t.l.Unlock()

	// If no configuration is available, we don't do anything
	if t.Config == nil {
		return nil
	}

	// Reset the uniqueness map. If we're tracking uniques, then populate
	// it with addresses.
	t.uniqueMap = make(map[string]struct{})
	defer func() { t.uniqueMap = nil }()
	if t.Unique {
		for _, v := range g.Vertices() {
			if rn, ok := v.(configName); ok {
				t.uniqueMap[rn.Name()] = struct{}{}
			}
		}
	}

	// Start the transformation process
	return t.transform(g, t.Config)
}

func (t *ConfigTransformer) transform(g *Graph, config *configs.Config) error {
	// If no config, do nothing
	if config == nil {
		return nil
	}

	// Add our resources
	if err := t.transformSingle(g, config); err != nil {
		return err
	}

	// Transform all the children.
	for _, c := range config.Children {
		if err := t.transform(g, c); err != nil {
			return err
		}
	}

	return nil
}

func (t *ConfigTransformer) transformSingle(g *Graph, config *configs.Config) error {
	path := config.Path
	module := config.Module
	log.Printf("[TRACE] ConfigTransformer: Starting for path: %v", path)

	allResources := make([]*configs.Resource, 0, len(module.ManagedResources)+len(module.DataResources))
	for _, r := range module.ManagedResources {
		allResources = append(allResources, r)
	}
	for _, r := range module.DataResources {
		allResources = append(allResources, r)
	}

	for _, r := range allResources {
		relAddr := r.Addr()

		if t.ModeFilter && relAddr.Mode != t.Mode {
			// Skip non-matching modes
			continue
		}

		abstract := &NodeAbstractResource{
			Addr: addrs.ConfigResource{
				Resource: relAddr,
				Module:   path,
			},
		}

		if _, ok := t.uniqueMap[abstract.Name()]; ok {
			// We've already seen a resource with this address. This should
			// never happen, because we enforce uniqueness in the config loader.
			continue
		}

		var node dag.Vertex = abstract
		if f := t.Concrete; f != nil {
			node = f(abstract)
		}

		g.Add(node)
	}

	return nil
}
