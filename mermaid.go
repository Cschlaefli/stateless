package stateless

// stateDiagram-v2 syntax reference: https://mermaid.js.org/syntax/stateDiagram.html
//
// Key syntax rules used here:
//   - Header:           stateDiagram-v2
//   - Direction:        direction LR
//   - State alias:      state "Display Name" as validId   (required when name is not a valid id)
//   - Simple state:     stateId                           (implicit on first use)
//   - Composite state:  state id { ... }
//   - Initial arrow:    [*] --> stateId
//   - Transition:       src --> dst : label
//
// Valid mermaid state IDs are ASCII alphanumeric + underscore and must not start with a digit.
// Non-conforming names are encoded via mermaidStateID and aliased with a state declaration.

//go:generate go test -run TestStateMachine_ToMermaid -update .

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type mermaidGraph struct{}

// toMermaidDiagram converts sm into a Mermaid stateDiagram-v2 string.
func toMermaidDiagram(sm *StateMachine) string {
	return new(mermaidGraph).formatStateMachine(sm)
}

func (m *mermaidGraph) formatStateMachine(sm *StateMachine) string {
	var sb strings.Builder
	sb.WriteString("stateDiagram-v2\n")
	sb.WriteString("    direction LR\n\n")

	stateList := m.sortedStateList(sm)

	// Alias declarations for states whose display names are not valid mermaid identifiers.
	for _, sr := range stateList {
		name := fmt.Sprint(sr.State)
		id := mermaidStateID(name)
		if id != name {
			sb.WriteString(fmt.Sprintf("    state \"%s\" as %s\n", mermaidEscLabel(name), id))
		}
	}

	// Initial-state arrow.
	if initialState, err := sm.State(context.Background()); err == nil {
		sb.WriteString(fmt.Sprintf("    [*] --> %s\n", mermaidStateID(fmt.Sprint(initialState))))
	}

	// Composite-state blocks (only for top-level states that have substates).
	for _, sr := range stateList {
		if sr.Superstate == nil && len(sr.Substates) > 0 {
			sb.WriteRune('\n')
			m.writeCompositeState(&sb, sm, sr, 1)
		}
	}

	// Transitions.
	sb.WriteRune('\n')
	for _, sr := range stateList {
		m.writeTransitions(&sb, sr)
	}

	return sb.String()
}

func (m *mermaidGraph) writeCompositeState(sb *strings.Builder, sm *StateMachine, sr *stateRepresentation, depth int) {
	indent := strings.Repeat("    ", depth)
	id := mermaidStateID(fmt.Sprint(sr.State))
	sb.WriteString(fmt.Sprintf("%sstate %s {\n", indent, id))

	// Internal initial-transition inside the composite state.
	if sr.HasInitialState {
		if dest := sm.stateConfig[sr.InitialTransitionTarget]; dest != nil {
			sb.WriteString(fmt.Sprintf("%s    [*] --> %s\n", indent, mermaidStateID(fmt.Sprint(dest.State))))
		}
	}

	for _, sub := range m.sortedSubstates(sr.Substates) {
		if len(sub.Substates) > 0 {
			// Nested composite state: recurse.
			m.writeCompositeState(sb, sm, sub, depth+1)
		} else {
			// Simple substate: declare by id so mermaid knows the hierarchy.
			sb.WriteString(fmt.Sprintf("%s    %s\n", indent, mermaidStateID(fmt.Sprint(sub.State))))
		}
	}

	sb.WriteString(indent + "}\n")
}

func (m *mermaidGraph) writeTransitions(sb *strings.Builder, sr *stateRepresentation) {
	srcID := mermaidStateID(fmt.Sprint(sr.State))

	triggers := make([]triggerBehaviour, 0)
	for _, ts := range sr.TriggerBehaviours {
		triggers = append(triggers, ts...)
	}
	slices.SortFunc(triggers, func(a, b triggerBehaviour) int {
		return strings.Compare(fmt.Sprint(a.GetTrigger()), fmt.Sprint(b.GetTrigger()))
	})

	for _, t := range triggers {
		switch tb := t.(type) {
		case *transitioningTriggerBehaviour:
			dstID := mermaidStateID(fmt.Sprint(tb.Destination))
			sb.WriteString(fmt.Sprintf("    %s --> %s : %s\n", srcID, dstID, m.fmtLabel(tb.Trigger, tb.Guard, "")))
		case *reentryTriggerBehaviour:
			dstID := mermaidStateID(fmt.Sprint(tb.Destination))
			sb.WriteString(fmt.Sprintf("    %s --> %s : %s\n", srcID, dstID, m.fmtLabel(tb.Trigger, tb.Guard, "🔄 ")))
		case *internalTriggerBehaviour:
			sb.WriteString(fmt.Sprintf("    %s --> %s : %s\n", srcID, srcID, m.fmtLabel(tb.Trigger, tb.Guard, "🔒 ")))
		case *ignoredTriggerBehaviour:
			sb.WriteString(fmt.Sprintf("    %s --> %s : %s\n", srcID, srcID, m.fmtLabel(tb.Trigger, tb.Guard, "🚫 ")))
		}
	}
}

func (m *mermaidGraph) fmtLabel(trigger Trigger, guard transitionGuard, prefix string) string {
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(mermaidEscLabel(fmt.Sprint(trigger)))
	for _, g := range guard.Guards {
		sb.WriteString(fmt.Sprintf(" [%s]", mermaidEscLabel(g.Description.String())))
	}
	return sb.String()
}

func (*mermaidGraph) sortedStateList(sm *StateMachine) []*stateRepresentation {
	list := make([]*stateRepresentation, 0, len(sm.stateConfig))
	for _, sr := range sm.stateConfig {
		list = append(list, sr)
	}
	slices.SortFunc(list, func(a, b *stateRepresentation) int {
		return strings.Compare(fmt.Sprint(a.State), fmt.Sprint(b.State))
	})
	return list
}

func (*mermaidGraph) sortedSubstates(subs []*stateRepresentation) []*stateRepresentation {
	out := make([]*stateRepresentation, len(subs))
	copy(out, subs)
	slices.SortFunc(out, func(a, b *stateRepresentation) int {
		return strings.Compare(fmt.Sprint(a.State), fmt.Sprint(b.State))
	})
	return out
}

// mermaidStateID converts a state name into a valid mermaid state identifier.
// Characters outside ASCII letters/digits/underscore are encoded as uXXXX to
// guarantee uniqueness and parser safety.
func mermaidStateID(name string) string {
	if name == "" {
		return "s_empty"
	}
	var sb strings.Builder
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				// Mermaid IDs must not start with a digit.
				sb.WriteString("s_")
			}
			sb.WriteRune(r)
		default:
			fmt.Fprintf(&sb, "u%04X", r)
		}
	}
	return sb.String()
}

// mermaidEscLabel sanitises a string for use as a mermaid transition or state label.
// Double quotes are replaced with single quotes since mermaid does not support
// escaped double quotes inside label strings.
func mermaidEscLabel(s string) string {
	return strings.ReplaceAll(s, `"`, `'`)
}
