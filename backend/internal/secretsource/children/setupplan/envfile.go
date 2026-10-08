package setupplan

import (
	"slices"
	"strings"
)

// RemoveEnvKeys removes the entries of keys from .env content and keeps
// everything else as written: comments, blank lines, order, and quoting.
// Quoted values that span several lines are removed whole. It returns the new
// content and the keys it removed, sorted.
func RemoveEnvKeys(content string, keys []string) (string, []string) {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}

	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	removedSet := map[string]struct{}{}
	for i := 0; i < len(lines); i++ {
		key, value, ok := splitEntryInternal(lines[i])
		if !ok {
			kept = append(kept, lines[i])
			continue
		}
		last := i
		if quote, open := openQuoteInternal(value); open {
			for last+1 < len(lines) && !closesQuoteInternal(lines[last+1], quote) {
				last++
			}
			if last+1 < len(lines) {
				last++
			}
		}
		if _, remove := drop[key]; remove {
			removedSet[key] = struct{}{}
		} else {
			kept = append(kept, lines[i:last+1]...)
		}
		i = last
	}

	removed := make([]string, 0, len(removedSet))
	for key := range removedSet {
		removed = append(removed, key)
	}
	slices.Sort(removed)
	return strings.Join(kept, "\n"), removed
}

// splitEntryInternal splits "KEY=value", "export KEY=value", or "KEY: value".
func splitEntryInternal(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "export ")
	sep := strings.IndexAny(trimmed, "=:")
	if sep <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:sep])
	if !envKeyPattern.MatchString(key) {
		return "", "", false
	}
	return key, strings.TrimLeft(trimmed[sep+1:], " \t"), true
}

// openQuoteInternal reports whether value starts a quoted string that does
// not close on the same line.
func openQuoteInternal(value string) (byte, bool) {
	if value == "" {
		return 0, false
	}
	quote := value[0]
	if quote != '"' && quote != '\'' && quote != '`' {
		return 0, false
	}
	return quote, !closesQuoteInternal(value[1:], quote)
}

// closesQuoteInternal reports whether line contains the closing quote. In
// double quotes a backslash escapes the next character.
func closesQuoteInternal(line string, quote byte) bool {
	for k := 0; k < len(line); k++ {
		switch {
		case quote == '"' && line[k] == '\\':
			k++
		case line[k] == quote:
			return true
		}
	}
	return false
}

// PlanEnvRemoval removes keys like RemoveEnvKeys, except keys that remaining
// entries still reference, such as DB_PASSWORD in
// DB_URL=postgres://app:${DB_PASSWORD}@db. Removing those would change the
// remaining values, so they stay and are returned as referenced.
func PlanEnvRemoval(content string, keys []string) (updated string, removed, referenced []string) {
	candidates := slices.Clone(keys)
	for {
		updated, removed = RemoveEnvKeys(content, candidates)
		refs := ReferencedKeys(updated)
		still := slices.DeleteFunc(slices.Clone(removed), func(key string) bool {
			_, used := refs[key]
			return !used
		})
		if len(still) == 0 {
			slices.Sort(referenced)
			return updated, removed, referenced
		}
		referenced = append(referenced, still...)
		candidates = slices.DeleteFunc(candidates, func(key string) bool { return slices.Contains(still, key) })
	}
}

// ReferencedKeys returns the variables referenced in the values of .env
// content, such as ${DB_PASSWORD} or $DB_PASSWORD. It is conservative: single
// quoted values, which dotenv does not expand, are scanned too.
func ReferencedKeys(content string) map[string]struct{} {
	refs := map[string]struct{}{}
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		_, value, ok := splitEntryInternal(lines[i])
		if !ok {
			continue
		}
		full := value
		if quote, open := openQuoteInternal(value); open {
			for i+1 < len(lines) {
				i++
				full += "\n" + lines[i]
				if closesQuoteInternal(lines[i], quote) {
					break
				}
			}
		}
		for _, ref := range scanReferencesInternal(full) {
			refs[ref.name] = struct{}{}
		}
	}
	return refs
}
