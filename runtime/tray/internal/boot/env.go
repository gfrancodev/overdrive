package boot

import "os"

func errMissingRuntime() error {
	return os.ErrInvalid
}

func mergeEnv(extra []string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	skip := map[string]struct{}{}
	for _, item := range extra {
		if key, _, ok := splitEnv(item); ok {
			skip[key] = struct{}{}
		}
	}
	for _, item := range base {
		key, _, ok := splitEnv(item)
		if ok {
			if _, drop := skip[key]; drop {
				continue
			}
		}
		out = append(out, item)
	}
	return append(out, extra...)
}

func splitEnv(item string) (string, string, bool) {
	for i := 0; i < len(item); i++ {
		if item[i] == '=' {
			return item[:i], item[i+1:], true
		}
	}
	return "", "", false
}
