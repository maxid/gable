package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Namespace is gable's per-subsystem naming configuration. The prefixes stay
// in the framework: gns only carries the namespace value and context, it does
// not know about caches, migrations or CosID.
type Namespace struct {
	Root       string `yaml:"root" json:"root"`
	Cache      string `yaml:"cache,omitempty" json:"cache,omitempty"`
	Flyway     string `yaml:"flyway,omitempty" json:"flyway,omitempty"`
	CosID      string `yaml:"cosid,omitempty" json:"cosid,omitempty"`
	Cluster    string `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	TimeSeries string `yaml:"timeSeries,omitempty" json:"timeSeries,omitempty"`
}

// DefaultNamespace returns a namespace rooted at gable. Subsystem values stay
// empty so environment overrides still inherit the final root.
func DefaultNamespace() Namespace { return Namespace{Root: "gable"} }

var namespaceRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Normalize fills empty subsystem values from Root.
func (n Namespace) Normalize() Namespace {
	if strings.TrimSpace(n.Root) == "" {
		n.Root = "gable"
	}
	n.Root = strings.TrimSpace(n.Root)
	for _, target := range []*string{&n.Cache, &n.Flyway, &n.CosID, &n.Cluster, &n.TimeSeries} {
		*target = strings.TrimSpace(*target)
		if *target == "" {
			*target = n.Root
		}
	}
	return n
}

// Validate checks every namespace that becomes part of a key or table name.
func (n Namespace) Validate() error {
	n = n.Normalize()
	if !namespaceRE.MatchString(n.Root) {
		return fmt.Errorf("namespace root %q must match ^[a-z][a-z0-9_-]*$", n.Root)
	}
	for label, value := range map[string]string{
		"cache": n.Cache, "cluster": n.Cluster, "timeSeries": n.TimeSeries,
	} {
		if !namespaceRE.MatchString(value) {
			return fmt.Errorf("namespace %s %q must match ^[a-z][a-z0-9_-]*$", label, value)
		}
	}
	if n.Flyway != "none" && !namespaceRE.MatchString(n.Flyway) {
		return fmt.Errorf("namespace flyway %q must match ^[a-z][a-z0-9_-]*$ or be \"none\"", n.Flyway)
	}
	if n.CosID != "none" && !namespaceRE.MatchString(n.CosID) {
		return fmt.Errorf("namespace cosid %q must match ^[a-z][a-z0-9_-]*$ or be \"none\"", n.CosID)
	}
	return nil
}

// CacheName is the namespace passed to gcache.
func (n Namespace) CacheName() string { return n.Cache }

// FlywayName is the migration history table prefix; "none" means no prefix.
func (n Namespace) FlywayName() string { return n.Flyway }

// FlywayTable renders the migration history table for base. "flyway: none"
// keeps the base name verbatim (e.g. an existing Flyway table).
func (n Namespace) FlywayTable(base string) string {
	base = sanitizeNamespace(base)
	if n.Flyway == "none" {
		return base
	}
	return n.Flyway + "_" + base
}

// CosIDName is the CosID allocator namespace. "cosid: none" returns the empty
// string so an allocator keeps the logical segment names it already has.
func (n Namespace) CosIDName() string {
	if n.CosID == "none" {
		return ""
	}
	return n.CosID
}

// ClusterName is the cluster/gossip namespace.
func (n Namespace) ClusterName() string { return n.Cluster }

// TimeSeriesName is the time-series table prefix.
func (n Namespace) TimeSeriesName() string { return n.TimeSeries }

// SegmentKey renders the fully-qualified CosID key. Pass the logical name to
// gcosid.NewSegmentGenerator and CosIDName to the allocator; do not pass this
// value to the generator when the allocator already has a namespace.
func (n Namespace) SegmentKey(logical string) string {
	if n.CosID == "none" || logical == "" {
		return logical
	}
	return n.CosID + "." + logical
}

// Table renders a time-series (or other engine-specific) table name.
func (n Namespace) Table(base string) string { return n.TimeSeries + "_" + sanitizeNamespace(base) }

// RedisKey prefixes a logical key with the cache namespace.
func (n Namespace) RedisKey(parts ...string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, n.Cache)
	for _, part := range parts {
		part = strings.Trim(part, ":")
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ":")
}

// Topic and Group render Kafka names using the cluster namespace.
func (n Namespace) Topic(logical string) string { return n.Cluster + "." + logical }
func (n Namespace) Group(logical string) string { return n.Cluster + "." + logical }

// Channel renders a Redis pub/sub or internal cluster channel.
func (n Namespace) Channel(logical string) string { return n.Cluster + ":" + logical }

func sanitizeNamespace(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return "default"
	}
	return out
}
