package main

import (
	"strings"
	"testing"
)

func TestBuildDeleteMutation(t *testing.T) {
	query, vars := buildDeleteMutation([]tagRef{
		{Name: "v1.0.0", ID: "REF_aaa"},
		{Name: "scratch", ID: "REF_bbb"},
	})

	for _, want := range []string{
		"mutation DeleteTags($ref0: ID!, $ref1: ID!)",
		"d0: deleteRef(input: {refId: $ref0}) { clientMutationId }",
		"d1: deleteRef(input: {refId: $ref1}) { clientMutationId }",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query is missing %q:\n%s", want, query)
		}
	}

	if len(vars) != 2 || vars["ref0"] != "REF_aaa" || vars["ref1"] != "REF_bbb" {
		t.Errorf("got variables %v, want ref0=REF_aaa ref1=REF_bbb", vars)
	}

	// Tag names must never reach the query text, only the ref IDs do.
	if strings.Contains(query, "v1.0.0") || strings.Contains(query, "scratch") {
		t.Errorf("tag names leaked into the query text:\n%s", query)
	}
}

func TestAliasIndex(t *testing.T) {
	cases := []struct {
		path []interface{}
		want int
		ok   bool
	}{
		{path: []interface{}{"d0"}, want: 0, ok: true},
		{path: []interface{}{"d17"}, want: 17, ok: true},
		{path: []interface{}{"d3", "clientMutationId"}, want: 3, ok: true},
		{path: []interface{}{"repository"}, ok: false},
		{path: []interface{}{42}, ok: false},
		{path: nil, ok: false},
	}

	for _, c := range cases {
		got, ok := aliasIndex(c.path)
		if ok != c.ok {
			t.Errorf("aliasIndex(%v) ok = %v, want %v", c.path, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("aliasIndex(%v) = %d, want %d", c.path, got, c.want)
		}
	}
}
