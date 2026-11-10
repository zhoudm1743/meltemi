package style

// Meta describes the identity and interactive state of a component so a
// sheet can be resolved against it. Type is the component type name
// (Column, Label, ...), ID and Class are the optional id and class names.
// The state flags report which interactive states are currently active.
type Meta struct {
	Type, ID, Class                          string
	Hover, Focus, Disabled, Checked, Pressed bool
}

// Resolved is the outcome of resolving a sheet for one component: the
// winning value of every property that applies to it.
type Resolved struct {
	Values map[string]string
}

// Selector priority levels, lowest to highest. A rule only contributes its
// properties when no matching rule sits at a higher level, and within one
// level later rules override earlier ones.
const (
	priorityType = iota + 1
	priorityTypeState
	priorityClass
	priorityClassState
	priorityID
	priorityIDState
)

// inheritedProperties lists the only properties a child picks up from its
// parent's resolved style. Everything else (background, border, padding,
// ...) is per-component and never inherited.
var inheritedProperties = map[string]bool{
	"font-family": true,
	"font-size":   true,
	"color":       true,
}

// Resolve computes the style for a component described by meta. Matching
// rules are ranked by selector priority (type < type+state < class <
// class+state < id < id+state); only the highest matching level contributes
// properties, and within that level later rules override earlier ones.
// Values for properties no rule sets fall back to inherited, but only
// font-family, font-size and color are inherited. Opacity is deliberately
// excluded: the UI multiplies it while drawing the child subtree instead.
// Inherited entries are copied so the result never aliases the inherited map.
// ID and class selectors never match an empty name. The returned Values map
// is always non-nil.
func (s *Sheet) Resolve(meta Meta, inherited Resolved) Resolved {
	priorities := make([]int, len(s.Rules))
	best := 0
	for i := range s.Rules {
		priorities[i] = matchPriority(s.Rules[i].Selector, meta)
		if priorities[i] > best {
			best = priorities[i]
		}
	}

	values := make(map[string]string)
	for k, v := range inherited.Values {
		if inheritedProperties[k] {
			values[k] = v
		}
	}
	if best == 0 {
		return Resolved{Values: values}
	}
	for i := range s.Rules {
		if priorities[i] != best {
			continue
		}
		for _, prop := range s.Rules[i].Properties {
			switch prop.Name {
			case "background":
				delete(values, "background-color")
			case "border":
				delete(values, "border-color")
				delete(values, "border-width")
			}
			values[prop.Name] = prop.Value
		}
	}
	return Resolved{Values: values}
}

// matchPriority ranks a selector against meta: 0 means no match, otherwise
// the selector priority level. State selectors additionally require the
// matching state flag; class and id selectors never match an empty name.
func matchPriority(sel Selector, meta Meta) int {
	hasState := sel.State != StateNone
	switch sel.Kind {
	case TypeSelector:
		if meta.Type != sel.Name {
			return 0
		}
		if !hasState {
			return priorityType
		}
		if stateActive(sel.State, meta) {
			return priorityTypeState
		}
	case ClassSelector:
		if meta.Class == "" || meta.Class != sel.Name {
			return 0
		}
		if !hasState {
			return priorityClass
		}
		if stateActive(sel.State, meta) {
			return priorityClassState
		}
	case IDSelector:
		if meta.ID == "" || meta.ID != sel.Name {
			return 0
		}
		if !hasState {
			return priorityID
		}
		if stateActive(sel.State, meta) {
			return priorityIDState
		}
	}
	return 0
}

// stateActive reports whether the state a selector targets is active in
// meta.
func stateActive(state State, meta Meta) bool {
	switch state {
	case StateHover:
		return meta.Hover
	case StateFocus:
		return meta.Focus
	case StateDisabled:
		return meta.Disabled
	case StateChecked:
		return meta.Checked
	case StatePressed:
		return meta.Pressed
	}
	return false
}
