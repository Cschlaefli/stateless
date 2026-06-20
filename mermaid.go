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
//   - Entry into sub:   [*] --> subID : trigger  (inside composite block)
//   - Exit to super:    subID --> [*] : trigger  (inside composite block)
//
// Valid mermaid state IDs are ASCII alphanumeric + underscore and must not start with a digit.
// Non-conforming names are encoded via mermaidStateID and aliased with a state declaration.
//
// Multiple transitions between the same (src, dst) pair are merged into one arrow
// with labels joined by " / " to avoid mermaid only rendering the last label.
// Transitions between a superstate and its direct substates are rendered inside
// the composite block as [*] → sub and sub → [*] for cleaner layout.

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

// mermaidSrcDst identifies a directed edge between two states.
type mermaidSrcDst struct{ src, dst State }

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

	// Build set of (src, dst) pairs that belong inside a composite block:
	// direct superstate→substate and substate→superstate edges.
	insideSet := m.buildInsideSet(stateList)

	// Composite-state blocks (only for top-level states that have substates).
	for _, sr := range stateList {
		if sr.Superstate == nil && len(sr.Substates) > 0 {
			sb.WriteRune('\n')
			m.writeCompositeState(&sb, sm, sr, 1, insideSet)
		}
	}

	// Notes for non-trigger-specific entry/exit/activate/deactivate actions.
	for _, sr := range stateList {
		m.writeStateNote(&sb, sr)
	}

	// Outer-level transitions (merged per src→dst pair).
	sb.WriteRune('\n')
	for _, sr := range stateList {
		m.writeTransitions(&sb, sm, sr, insideSet)
	}

	return sb.String()
}

// buildInsideSet returns the set of (src, dst) state pairs that should be
// rendered inside a composite block rather than at the top level.
func (m *mermaidGraph) buildInsideSet(stateList []*stateRepresentation) map[mermaidSrcDst]bool {
	set := make(map[mermaidSrcDst]bool)
	for _, sr := range stateList {
		for _, sub := range sr.Substates {
			set[mermaidSrcDst{sr.State, sub.State}] = true
			set[mermaidSrcDst{sub.State, sr.State}] = true
		}
	}
	return set
}

func (m *mermaidGraph) writeCompositeState(sb *strings.Builder, sm *StateMachine, sr *stateRepresentation, depth int, insideSet map[mermaidSrcDst]bool) {
	indent := strings.Repeat("    ", depth)
	id := mermaidStateID(fmt.Sprint(sr.State))
	sb.WriteString(fmt.Sprintf("%sstate %s {\n", indent, id))

	// InitialTransition declared via Configure().InitialTransition().
	if sr.HasInitialState {
		if dest := sm.stateConfig[sr.InitialTransitionTarget]; dest != nil {
			sb.WriteString(fmt.Sprintf("%s    [*] --> %s\n", indent, mermaidStateID(fmt.Sprint(dest.State))))
		}
	}

	// Declare substates (recurse for nested composites, simple id for leaves).
	for _, sub := range m.sortedSubstates(sr.Substates) {
		if len(sub.Substates) > 0 {
			m.writeCompositeState(sb, sm, sub, depth+1, insideSet)
		} else {
			sb.WriteString(fmt.Sprintf("%s    %s\n", indent, mermaidStateID(fmt.Sprint(sub.State))))
		}
	}

	// Transitions that cross the superstate/substate boundary:
	//   superstate → substate becomes  [*] --> sub : trigger
	//   substate → superstate becomes  sub --> [*] : trigger
	for _, sub := range m.sortedSubstates(sr.Substates) {
		subID := mermaidStateID(fmt.Sprint(sub.State))
		for _, label := range m.collectTransitionLabels(sm, sr, sub.State) {
			sb.WriteString(fmt.Sprintf("%s    [*] --> %s : %s\n", indent, subID, label))
		}
		for _, label := range m.collectTransitionLabels(sm, sub, sr.State) {
			sb.WriteString(fmt.Sprintf("%s    %s --> [*] : %s\n", indent, subID, label))
		}
	}

	sb.WriteString(indent)
	sb.WriteString("}\n")
}

// collectTransitionLabels returns one formatted label per trigger in sr that
// transitions to dst. Trigger-specific entry actions on the destination are
// included inline in the label.
func (m *mermaidGraph) collectTransitionLabels(sm *StateMachine, sr *stateRepresentation, dst State) []string {
	var labels []string
	for _, t := range m.sortedTriggers(sr) {
		switch tb := t.(type) {
		case *transitioningTriggerBehaviour:
			if tb.Destination == dst {
				actions := m.getEntryActions(sm, tb.Destination, tb.Trigger)
				labels = append(labels, m.fmtLabel(tb.Trigger, tb.Guard, "", actions))
			}
		case *reentryTriggerBehaviour:
			if tb.Destination == dst {
				actions := m.getEntryActions(sm, tb.Destination, tb.Trigger)
				labels = append(labels, m.fmtLabel(tb.Trigger, tb.Guard, "🔄 ", actions))
			}
		}
	}
	return labels
}

// writeTransitions emits outer-level transitions for sr, skipping pairs that
// are inside a composite block. Multiple transitions to the same destination
// are merged into one arrow with labels joined by " / ".
func (m *mermaidGraph) writeTransitions(sb *strings.Builder, sm *StateMachine, sr *stateRepresentation, insideSet map[mermaidSrcDst]bool) {
	srcID := mermaidStateID(fmt.Sprint(sr.State))

	// Collect labels grouped by destination, preserving first-seen order.
	type dstEntry struct {
		dstID  string
		labels []string
	}
	var order []State
	byDst := make(map[string]*dstEntry)

	add := func(dst State, label string) {
		dstID := mermaidStateID(fmt.Sprint(dst))
		if _, exists := byDst[dstID]; !exists {
			order = append(order, dst)
			byDst[dstID] = &dstEntry{dstID: dstID}
		}
		byDst[dstID].labels = append(byDst[dstID].labels, label)
	}

	for _, t := range m.sortedTriggers(sr) {
		switch tb := t.(type) {
		case *transitioningTriggerBehaviour:
			if !insideSet[mermaidSrcDst{sr.State, tb.Destination}] {
				actions := m.getEntryActions(sm, tb.Destination, tb.Trigger)
				add(tb.Destination, m.fmtLabel(tb.Trigger, tb.Guard, "", actions))
			}
		case *reentryTriggerBehaviour:
			if !insideSet[mermaidSrcDst{sr.State, tb.Destination}] {
				actions := m.getEntryActions(sm, tb.Destination, tb.Trigger)
				add(tb.Destination, m.fmtLabel(tb.Trigger, tb.Guard, "🔄 ", actions))
			}
		case *internalTriggerBehaviour:
			add(sr.State, m.fmtLabel(tb.Trigger, tb.Guard, "🔒 ", nil))
		case *ignoredTriggerBehaviour:
			add(sr.State, m.fmtLabel(tb.Trigger, tb.Guard, "🚫 ", nil))
		}
	}

	for _, dst := range order {
		dstID := mermaidStateID(fmt.Sprint(dst))
		e := byDst[dstID]
		label := strings.Join(e.labels, " / ")
		sb.WriteString(fmt.Sprintf("    %s --> %s : %s\n", srcID, dstID, label))
	}
}

// writeStateNote emits a "note right of" block for all non-trigger-specific
// entry/exit/activate/deactivate actions on sr.
func (m *mermaidGraph) writeStateNote(sb *strings.Builder, sr *stateRepresentation) {
	lines := m.formatActionLines(sr)
	if len(lines) == 0 {
		return
	}
	id := mermaidStateID(fmt.Sprint(sr.State))
	sb.WriteString(fmt.Sprintf("    note right of %s\n", id))
	for _, l := range lines {
		sb.WriteString(fmt.Sprintf("        %s\n", l))
	}
	sb.WriteString("    end note\n")
}

// formatActionLines returns display lines for all non-trigger-specific actions
// on a state, mirroring the order used in graph.go's formatActions.
func (m *mermaidGraph) formatActionLines(sr *stateRepresentation) []string {
	var es []string
	for _, act := range sr.ActivateActions {
		es = append(es, "activated / "+mermaidEscLabel(act.Description.String()))
	}
	for _, act := range sr.DeactivateActions {
		es = append(es, "deactivated / "+mermaidEscLabel(act.Description.String()))
	}
	for _, act := range sr.EntryActions {
		if act.Trigger == nil {
			es = append(es, "entry / "+mermaidEscLabel(act.Description.String()))
		}
	}
	for _, act := range sr.ExitActions {
		es = append(es, "exit / "+mermaidEscLabel(act.Description.String()))
	}
	return es
}

// getEntryActions returns the descriptions of trigger-specific entry actions
// on the destination state for the given trigger.
func (m *mermaidGraph) getEntryActions(sm *StateMachine, dst State, t Trigger) []string {
	dest := sm.stateConfig[dst]
	if dest == nil {
		return nil
	}
	var actions []string
	for _, ea := range dest.EntryActions {
		if ea.Trigger != nil && *ea.Trigger == t {
			actions = append(actions, mermaidEscLabel(ea.Description.String()))
		}
	}
	return actions
}

func (m *mermaidGraph) fmtLabel(trigger Trigger, guard transitionGuard, prefix string, actions []string) string {
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(mermaidEscLabel(fmt.Sprint(trigger)))
	if len(actions) > 0 {
		sb.WriteString(" / ")
		sb.WriteString(strings.Join(actions, ", "))
	}
	for _, g := range guard.Guards {
		sb.WriteString(fmt.Sprintf(" [%s]", mermaidEscLabel(g.Description.String())))
	}
	return sb.String()
}

func (m *mermaidGraph) sortedTriggers(sr *stateRepresentation) []triggerBehaviour {
	triggers := make([]triggerBehaviour, 0, len(sr.TriggerBehaviours))
	for _, ts := range sr.TriggerBehaviours {
		triggers = append(triggers, ts...)
	}
	slices.SortFunc(triggers, func(a, b triggerBehaviour) int {
		return strings.Compare(fmt.Sprint(a.GetTrigger()), fmt.Sprint(b.GetTrigger()))
	})
	return triggers
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
