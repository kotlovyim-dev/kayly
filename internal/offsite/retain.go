package offsite

import (
	"strings"
	"time"
)

const (
	keepAll   = 7 * 24 * time.Hour
	keepWeeks = 4
)

// Object is a remote backup. Name is "<project>/<time>-<reason>.<kind>".
type Object struct {
	Key  string
	Name string
	Time time.Time
}

// kind groups objects that replace each other: the project and the part of the file name
// after its first dot.
func (o Object) kind() string {
	dir, file, _ := strings.Cut(o.Name, "/")
	_, kind, _ := strings.Cut(file, ".")
	return dir + "/" + kind
}

// Expired returns the objects retention no longer needs (ARCHITECTURE.md §8): everything from
// the last 7 days is kept, then the newest of each week for 4 weeks, then nothing.
func Expired(objs []Object, now time.Time) []Object {
	type slot struct {
		kind string
		week int
	}
	// week reports which week past the 7 days an object falls in, or -1 if it is kept whole
	// (recent) and keepWeeks if it is too old to keep at all.
	week := func(o Object) int {
		age := now.Sub(o.Time)
		if age < keepAll {
			return -1
		}
		return min(int((age-keepAll)/(7*24*time.Hour)), keepWeeks)
	}
	newest := map[slot]Object{}
	for _, o := range objs {
		w := week(o)
		if w < 0 || w >= keepWeeks {
			continue
		}
		if cur, ok := newest[slot{o.kind(), w}]; !ok || o.Time.After(cur.Time) {
			newest[slot{o.kind(), w}] = o
		}
	}
	var out []Object
	for _, o := range objs {
		w := week(o)
		if w >= keepWeeks || (w >= 0 && newest[slot{o.kind(), w}].Key != o.Key) {
			out = append(out, o)
		}
	}
	return out
}
