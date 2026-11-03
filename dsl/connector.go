package dsl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maxid/gable/orm"
)

// Connectors resolves the database a model lives in.
//
// A connector is a name for "driver + DSN + pool" — the same idea as Yao's connector, minus the
// part where the definition is code. A model names one; a model that names nothing uses the
// default. The point is that the *definition* of a model does not change when it points at a
// different database.
//
// A dialect is not a connector: the dialect decides SQL syntax, the connector decides which
// database that syntax is sent to. One deployment routinely has one dialect and several
// connectors.
type Connectors struct {
	byName map[string]*orm.DB
	def    *orm.DB
}

// ConnectorName is the name a model uses to mean "the default connector".
const ConnectorName = "default"

// NewConnectors returns a set whose default is db.
func NewConnectors(db *orm.DB) *Connectors {
	return &Connectors{byName: map[string]*orm.DB{ConnectorName: db}, def: db}
}

// Add registers a named connector.
//
// A duplicate name and an empty one are wiring errors: silently replacing a connector would
// send a model's queries to a database nobody wrote down.
func (c *Connectors) Add(name string, db *orm.DB) error {
	name = strings.TrimSpace(name)
	if db == nil {
		return fmt.Errorf("dsl: connector %q has no database", name)
	}
	if !resourceNameRE.MatchString(name) {
		return fmt.Errorf("dsl: connector name %q must match %s", name, resourceNameRE)
	}
	if _, exists := c.byName[name]; exists {
		return fmt.Errorf("dsl: connector %q is already registered", name)
	}
	c.byName[name] = db
	return nil
}

// Default returns the default database.
func (c *Connectors) Default() *orm.DB { return c.def }

// Get returns a connector by name; the empty name is the default.
func (c *Connectors) Get(name string) (*orm.DB, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = ConnectorName
	}
	db, ok := c.byName[name]
	return db, ok
}

// Names lists the registered connectors, sorted.
func (c *Connectors) Names() []string {
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// For returns the database a model lives in.
func (c *Connectors) For(doc *ModelDoc) (*orm.DB, error) {
	if doc == nil {
		return nil, fmt.Errorf("dsl: nil model")
	}
	name := strings.TrimSpace(doc.Spec.Connector)
	db, ok := c.Get(name)
	if !ok {
		return nil, fmt.Errorf("dsl: model %q names connector %q, which is not registered (have %s)",
			doc.Metadata.Name, name, strings.Join(c.Names(), ", "))
	}
	return db, nil
}

// Validate reports models that name a connector this set does not have.
//
// It runs when a set is bound — construction and every reload — so a typo is a startup error
// rather than a 500 on the first request against that model.
func (c *Connectors) Validate(set *Set) Issues {
	if set == nil {
		return nil
	}
	var issues Issues
	for _, name := range sortedKeys(set.Models) {
		doc := set.Models[name]
		if _, err := c.For(doc); err != nil {
			issues = append(issues, Issue{Resource: resourceKey(KindModel, name), Field: "spec.connector", Message: err.Error()})
		}
	}
	return issues
}
