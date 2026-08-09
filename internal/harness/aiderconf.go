package harness

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// aiderReadKey is the aider config option that loads a read-only file into
// every session. Aider has no "conventions" option; this is the mechanism its
// docs point at for a conventions file.
const aiderReadKey = "read"

// mergeAiderRead adds path to the `read` list of an aider config document,
// returning the new document.
//
// The file belongs to the user, so this is a merge and not a render: every other
// key, the key order, and any comments survive untouched. That rules out the
// obvious map[string]any round-trip, which drops comments and reorders keys, so
// the work is done on the yaml.Node tree instead.
//
// `read` may legitimately be absent, a single scalar, or a list, since aider
// accepts a repeated flag as either. All three are handled, and an entry already
// present is left alone so repeated applies do not accumulate duplicates.
//
// Pass nil or empty existing for a file that does not exist yet.
func mergeAiderRead(existing []byte, path string) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := yaml.Unmarshal(existing, &doc); err != nil {
			// Refuse to touch a document weft cannot parse. Rewriting it would
			// destroy config the user owns, which is the whole reason this merges
			// rather than renders.
			return nil, fmt.Errorf("parsing aider config: %w", err)
		}
	}

	root, err := aiderConfRoot(&doc)
	if err != nil {
		return nil, err
	}
	if err := setReadEntry(root, path); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encoding aider config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding aider config: %w", err)
	}
	return buf.Bytes(), nil
}

// aiderConfRoot returns the document's top-level mapping, creating the document
// and mapping when the file was empty or held only comments.
func aiderConfRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
		return root, nil
	}
	root := doc.Content[0]
	// A config that parses as something other than a mapping (a bare list, a
	// scalar) is not an aider config. Editing it would be a guess.
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("aider config is not a YAML mapping")
	}
	return root, nil
}

// setReadEntry adds path to the mapping's `read` value, normalising a scalar to
// a list on the way. It is a no-op when path is already present.
func setReadEntry(root *yaml.Node, path string) error {
	entry := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: path}

	// Mapping nodes hold alternating key/value children.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != aiderReadKey {
			continue
		}
		val := root.Content[i+1]
		switch val.Kind {
		case yaml.ScalarNode:
			if val.Value == path {
				return nil
			}
			// Promote the single value to a list holding both. Copying the node
			// keeps the user's original entry with its comments attached.
			old := *val
			*val = yaml.Node{
				Kind:    yaml.SequenceNode,
				Tag:     "!!seq",
				Content: []*yaml.Node{&old, entry},
			}
			return nil
		case yaml.SequenceNode:
			for _, item := range val.Content {
				if item.Value == path {
					return nil
				}
			}
			val.Content = append(val.Content, entry)
			return nil
		default:
			return fmt.Errorf("aider config %q is neither a string nor a list", aiderReadKey)
		}
	}

	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: aiderReadKey},
		&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{entry}},
	)
	return nil
}
