package graph

import "regexp"

var idRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// ValidID 校验 ID 形态；空串或超长即非法（见 11 §2.4）。
func ValidID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}
	return idRe.MatchString(id)
}
