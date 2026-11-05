package harness

import (
	"slices"
)

// claudeHookEvents are the hook events Claude Code 2.1.309 runs, as its hooks
// reference lists them (https://code.claude.com/docs/en/hooks#hook-lifecycle).
// Claude Code matches event names exactly, case included: its agent loader
// drops any other key of an agent's hooks map with "unknown hook event; entry
// ignored". TestClaudeHookEventsMatchTheDocs checks this list against the
// current reference every night.
var claudeHookEvents = []string{
	"PreToolUse", "PostToolUse", "PostToolUseFailure", "PostToolBatch", "Notification",
	"UserPromptSubmit", "UserPromptExpansion", "SessionStart", "SessionEnd", "Stop",
	"StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact",
	"PreModelSwitch", "PostModelSwitch", "PermissionRequest", "PermissionDenied", "Setup",
	"TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult",
	"ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged",
	"FileChanged", "DirectoryAdded", "MessageDisplay",
}

// claudeGuardEvents are the events whose hooks can deny a tool call.
var claudeGuardEvents = []string{"PreToolUse", "PermissionRequest"}

// ClaudeIgnoredHookEvents returns the event names of a decoded hooks field
// when Claude Code ignores every entry, so the field does what omitting it
// does: no key is an event Claude Code runs, and no entry holds what Claude
// Code reads as PreToolUse or PermissionRequest hooks, which keep it from
// loading the agent at all. It mirrors Claude Code 2.1.309's agent loader;
// anything that is not a map of event names (a list, a string) also keeps the
// agent from loading.
func ClaudeIgnoredHookEvents(value any) ([]string, bool) {
	hooks, ok := value.(map[string]any)
	if !ok || len(hooks) == 0 || claudeHookMatcher(hooks) {
		return nil, false
	}
	events := sortedKeys(hooks)
	for _, event := range events {
		if slices.Contains(claudeHookEvents, event) {
			return nil, false
		}
		_, list := hooks[event].([]any)
		if claudeHidesGuard(hooks[event], 3, !list, false) {
			return nil, false
		}
	}
	return events, true
}

// claudeHoldsGuard reports whether value is a map with non-empty PreToolUse or
// PermissionRequest hooks.
func claudeHoldsGuard(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, event := range claudeGuardEvents {
		hooks, declared := fields[event]
		if list, isList := hooks.([]any); !declared || hooks == nil || isList && len(list) == 0 {
			continue
		}
		return true
	}
	return false
}

// claudeHookMatcher reports whether value reads as a hook matcher: a map with
// a non-empty hooks list, or a hooks map beside a matcher or with a type.
func claudeHookMatcher(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, hasMatcher := fields["matcher"]
	if !hasMatcher {
		for key := range fields {
			if slices.Contains(claudeHookEvents, key) {
				return false
			}
		}
	}
	switch hooks := fields["hooks"].(type) {
	case []any:
		return len(hooks) > 0
	case map[string]any:
		_, typed := hooks["type"].(string)
		return hasMatcher || typed
	}
	return false
}

// claudeHidesGuard reports whether value holds, within depth levels, guard
// hooks or (when matchers count) a hook matcher, the way Claude Code scans an
// entry it does not run before ignoring it.
func claudeHidesGuard(value any, depth int, matchersCount, inHooksList bool) bool {
	if claudeHoldsGuard(value) || matchersCount && claudeHookMatcher(value) {
		return true
	}
	if depth == 0 {
		return false
	}
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if claudeHidesGuard(item, depth-1, matchersCount, inHooksList) {
				return true
			}
		}
	case map[string]any:
		if _, typed := value["type"].(string); inHooksList && typed {
			return false
		}
		for key, item := range value {
			_, list := item.([]any)
			if claudeHidesGuard(item, depth-1,
				matchersCount && !slices.Contains(claudeHookEvents, key),
				key == "hooks" && list) {
				return true
			}
		}
	}
	return false
}
