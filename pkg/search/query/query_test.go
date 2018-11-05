// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package query

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var _ = log.Println

func TestQueryString(t *testing.T) {
	q := &Or{[]Q{
		&And{[]Q{
			&Substring{Pattern: "hoi"},
			&Not{&Substring{Pattern: "hai"}},
		}}}}
	got := q.String()
	want := `(or (and substr:"hoi" (not substr:"hai")))`

	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSimplify(t *testing.T) {
	type testcase struct {
		in   Q
		want Q
	}

	cases := []testcase{
		{
			in: NewOr(
				NewOr(
					NewAnd(&Substring{Pattern: "hoi"},
						&Not{&Substring{Pattern: "hai"}}),
					NewOr(
						&Substring{Pattern: "zip"},
						&Substring{Pattern: "zap"},
					))),
			want: NewOr(
				NewAnd(
					&Substring{Pattern: "hoi"},
					&Not{&Substring{Pattern: "hai"}}),
				&Substring{Pattern: "zip"},
				&Substring{Pattern: "zap"}),
		},
		{in: &And{}, want: &Const{true}},
		{in: &Or{}, want: &Const{false}},
		{in: NewAnd(&Const{true}, &Const{false}), want: &Const{false}},
		{in: NewOr(&Const{false}, &Const{true}), want: &Const{true}},
		{in: &Not{&Const{true}}, want: &Const{false}},
		{
			in: NewAnd(
				&Substring{Pattern: "byte"},
				&Not{NewAnd(&Substring{Pattern: "byte"})}),
			want: NewAnd(
				&Substring{Pattern: "byte"},
				&Not{&Substring{Pattern: "byte"}}),
		},
	}

	for _, c := range cases {
		got := Simplify(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
}

func TestMap(t *testing.T) {
	in := NewAnd(&Substring{Pattern: "bla"}, &Not{&Repo{"foo"}})
	out := NewAnd(&Substring{Pattern: "bla"}, &Not{&Const{false}})

	f := func(q Q) Q {
		if _, ok := q.(*Repo); ok {
			return &Const{false}
		}
		return q
	}
	got := Map(in, f)
	if !reflect.DeepEqual(got, out) {
		t.Errorf("got %v, want %v", got, out)
	}
}

func createListFunc(repos []string) func([]string, []string) (map[string]bool, error) {
	return func(inc, exc []string) (map[string]bool, error) {
		set := map[string]bool{}
		for _, r := range repos {
			set[r] = true
		}
		for r := range set {
			for _, re := range inc {
				if matched, err := regexp.MatchString(re, r); err != nil {
					return nil, err
				} else if !matched {
					delete(set, r)
				}
			}
			for _, re := range exc {
				if matched, err := regexp.MatchString(re, r); err != nil {
					return nil, err
				} else if matched {
					delete(set, r)
				}
			}
		}
		return set, nil
	}
}

func TestExpandRepo(t *testing.T) {
	list := createListFunc([]string{"foo", "bar", "baz"})

	cases := map[string]string{
		"r:":                         "(reposet bar baz foo)",
		"r:b":                        "(reposet bar baz)",
		"r:b r:a":                    "(reposet bar baz)",
		"r:b -r:baz":                 "(reposet bar)",
		"-r:f":                       "(reposet bar baz)",
		"r:foo":                      "(reposet foo)",
		"r:foo r:baz":                "FALSE",
		"r:foo test":                 "(and substr:\"test\" (reposet foo))",
		"r:foo test -hello":          "(and substr:\"test\" (not substr:\"hello\") (reposet foo))",
		"(r:ba test (r:b r:a -r:z))": "(and substr:\"test\" (reposet bar))",

		// Our only case where a reposet is a child of a not.
		"bar -(r:foo test)": "(and substr:\"bar\" (not (and substr:\"test\" (reposet foo))))",
	}
	for qStr, want := range cases {
		q, err := Parse(qStr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ExpandRepo(q, list)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Errorf("expandRepo(%q) got %s want %s", qStr, got.String(), want)
		}
	}
}

func TestExpandRepo_error(t *testing.T) {
	list := func(inc, exc []string) (map[string]bool, error) {
		return nil, errors.New("fail")
	}
	q, err := Parse("(foo repo:bar) or (baz repo:bam)")
	if err != nil {
		t.Fatal(err)
	}
	q, err = ExpandRepo(q, list)
	if err == nil {
		t.Fatalf("expected error, got %s", q.String())
	}
}

func TestMinimalRepoSet(t *testing.T) {
	list := createListFunc([]string{"foo", "bar", "baz"})

	cases := map[string]string{
		"r:":                                  "{bar baz foo}",
		"r:b":                                 "{bar baz}",
		"r:b r:a":                             "{bar baz}",
		"r:b -r:baz":                          "{bar}",
		"-r:f":                                "{bar baz}",
		"r:foo":                               "{foo}",
		"r:foo r:baz":                         "{}",
		"foo -(r:foo r:baz)":                  "",
		"foo r:ba -(r:foo or r:baz)":          "{bar}",
		"foo r:ba -(r:foo or r:baz or hello)": "{bar}",
		"foo r:ba -(r:foo or hello)":          "{bar baz}",
		"foo -(r:baz hello)":                  "",

		"foo r:ba -((r:foo or hello) world)": "{bar baz}",
		"foo r:ba -((r:foo or hello) r:foo)": "{bar baz}",

		"foo -(-(r:bar hello))": "{bar}",
		"foo -(-(hello))":       "",
		"foo -(r:bar hello)":    "",
		"foo -(hello)":          "",

		"foo r:b -(-(r:bar hello))": "{bar}",
		"foo r:b -(-(hello))":       "{bar baz}",
		"foo r:b -(r:bar hello)":    "{bar baz}",
		"foo r:b -(hello)":          "{bar baz}",

		// baz is still allowed since it can have matches in documents without
		// hello.
		"foo r:ba -(r:baz hello)":    "{bar baz}",
		"r:foo test":                 "{foo}",
		"r:foo test -hello":          "{foo}",
		"(r:ba test (r:b r:a -r:z))": "{bar}",

		// Our only case where a reposet is a child of a not.
		"bar -(r:foo test)": "",
	}
	for qStr, want := range cases {
		q, err := Parse(qStr)
		if err != nil {
			t.Fatal(err)
		}
		q, err = ExpandRepo(q, list)
		if err != nil {
			t.Fatal(err)
		}
		set, ok := MinimalRepoSet(q)
		got := ""
		if ok {
			keys := []string{}
			for k := range set {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			got = fmt.Sprintf("{%s}", strings.Join(keys, " "))
		}
		if got != want {
			t.Errorf("MinimalRepoSet(%q) got %v want %v", qStr, got, want)
		}
	}
}

func TestRepoQuery(t *testing.T) {
	cases := map[string]string{
		"r:":                                  "{bar baz foo}",
		"r:b":                                 "{bar baz}",
		"r:b r:a":                             "{bar baz}",
		"r:b -r:baz":                          "{bar}",
		"-r:f":                                "{bar baz}",
		"r:foo":                               "{foo}",
		"r:foo r:baz":                         "{}",
		"foo -(r:foo r:baz)":                  "",
		"foo r:ba -(r:foo or r:baz)":          "{bar}",
		"foo r:ba -(r:foo or r:baz or hello)": "{bar}",
		"foo r:ba -(r:foo or hello)":          "{bar baz}",
		"foo -(r:baz hello)":                  "",

		"foo r:ba -((r:foo or hello) world)": "{bar baz}",
		"foo r:ba -((r:foo or hello) r:foo)": "{bar baz}",

		"foo -(-(r:bar hello))": "{bar}",
		"foo -(-(hello))":       "",
		"foo -(r:bar hello)":    "",
		"foo -(hello)":          "",

		"foo r:b -(-(r:bar hello))": "{bar}",
		"foo r:b -(-(hello))":       "{bar baz}",
		"foo r:b -(r:bar hello)":    "{bar baz}",
		"foo r:b -(hello)":          "{bar baz}",

		// baz is still allowed since it can have matches in documents without
		// hello.
		"foo r:ba -(r:baz hello)":    "{bar baz}",
		"r:foo test":                 "{foo}",
		"r:foo test -hello":          "{foo}",
		"(r:ba test (r:b r:a -r:z))": "{bar}",

		// Our only case where a reposet is a child of a not.
		"bar -(r:foo test)": "",
	}
	for qStr, want := range cases {
		q, err := Parse(qStr)
		if err != nil {
			t.Fatal(err)
		}
		q = Simplify(q)
		rq := RepoQuery(q)
		fmt.Printf("%s\n%s\n%s\n\n", q, fmt.Sprintf(rq.Query(PrintfBindVar{}), rq.Args()...), want)
	}
}

type PrintfBindVar struct{}

func (PrintfBindVar) BindVar(i int) string {
	return "%q"
}

func TestVisitAtoms(t *testing.T) {
	in := NewAnd(&Substring{}, &Repo{}, &Not{&Const{}})
	count := 0
	VisitAtoms(in, func(q Q) {
		count++
	})
	if count != 3 {
		t.Errorf("got %d, want 3", count)
	}
}