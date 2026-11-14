// Package difflib is a port of the parts of CPython's Lib/difflib.py (3.14) that
// unified_diff needs: SequenceMatcher(None, a, b) with autojunk off.
//
// lazy: no isjunk, no ratio/quick_ratio, no context/ndiff; only what shortcut's
// diff_files calls. Upgrade path: port the rest from the same file if needed.
package difflib

import "sort"

// op is one edit operation. Tag is 'r' replace, 'd' delete, 'i' insert, 'e' equal.
type op struct {
	Tag            byte
	I1, I2, J1, J2 int
}

// match is a block of lines that a and b share.
type match struct{ A, B, Size int }

type matcher struct {
	a, b []string
	b2j  map[string][]int
	// rowA, rowB are the scratch rows of longest; steps counts work against budget.
	rowA, rowB []int
	steps      int
}

// newMatcher indexes b by line, with no junk function and no autojunk.
func newMatcher(a, b []string) *matcher {
	m := &matcher{a: a, b: b, b2j: map[string][]int{}, rowA: make([]int, len(b)+1), rowB: make([]int, len(b)+1)}
	for i, s := range b {
		m.b2j[s] = append(m.b2j[s], i)
	}
	return m
}

// budget bounds the inner-loop steps of one whole match. Without autojunk, a file where old and new share only
// frequent lines costs cubic time. Past the budget the rest is not matched and shows as one replace.
const budget = 10_000_000

// longest finds the longest matching block in a[alo:ahi] and b[blo:bhi]. With no junk set, only the popular-element
// extension loops of the original remain. It returns a zero-size match once the budget is spent.
func (m *matcher) longest(alo, ahi, blo, bhi int) match {
	besti, bestj, bestsize := alo, blo, 0
	// j2len[j+1] is the length of the match ending at b[j] for the previous line of a; two slices swap each row.
	j2len, newj2len := m.rowA, m.rowB
	var prev []int
	for i := alo; i < ahi; i++ {
		cur := m.b2j[m.a[i]]
		m.steps += 1 + len(cur) + len(prev)
		if m.steps > budget {
			clear(j2len)
			clear(newj2len)
			return match{alo, blo, 0}
		}
		for _, j := range cur {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j] + 1
			newj2len[j+1] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		for _, j := range prev {
			j2len[j+1] = 0
		}
		prev = cur
		j2len, newj2len = newj2len, j2len
	}
	for _, j := range prev {
		j2len[j+1] = 0
	}
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return match{besti, bestj, bestsize}
}

// blocks returns the matching blocks.
func (m *matcher) blocks() []match {
	la, lb := len(m.a), len(m.b)
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, la, 0, lb}}
	var found []match
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x := m.longest(q.alo, q.ahi, q.blo, q.bhi)
		if x.Size == 0 {
			continue
		}
		found = append(found, x)
		if q.alo < x.A && q.blo < x.B {
			queue = append(queue, span{q.alo, x.A, q.blo, x.B})
		}
		if x.A+x.Size < q.ahi && x.B+x.Size < q.bhi {
			queue = append(queue, span{x.A + x.Size, q.ahi, x.B + x.Size, q.bhi})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		x, y := found[i], found[j]
		if x.A != y.A {
			return x.A < y.A
		}
		if x.B != y.B {
			return x.B < y.B
		}
		return x.Size < y.Size
	})
	var out []match
	i1, j1, k1 := 0, 0, 0
	for _, f := range found {
		if i1+k1 == f.A && j1+k1 == f.B {
			k1 += f.Size
			continue
		}
		if k1 > 0 {
			out = append(out, match{i1, j1, k1})
		}
		i1, j1, k1 = f.A, f.B, f.Size
	}
	if k1 > 0 {
		out = append(out, match{i1, j1, k1})
	}
	return append(out, match{la, lb, 0})
}

// opcodes returns the edit operations that turn a into b.
func (m *matcher) opcodes() []op {
	var answer []op
	i, j := 0, 0
	for _, mb := range m.blocks() {
		var tag byte
		switch {
		case i < mb.A && j < mb.B:
			tag = 'r'
		case i < mb.A:
			tag = 'd'
		case j < mb.B:
			tag = 'i'
		}
		if tag != 0 {
			answer = append(answer, op{tag, i, mb.A, j, mb.B})
		}
		i, j = mb.A+mb.Size, mb.B+mb.Size
		if mb.Size > 0 {
			answer = append(answer, op{'e', mb.A, i, mb.B, j})
		}
	}
	return answer
}

// groupedOpcodes groups the opcodes into hunks with n lines of context.
func (m *matcher) groupedOpcodes(n int) [][]op {
	codes := m.opcodes()
	if len(codes) == 0 {
		codes = []op{{'e', 0, 1, 0, 1}}
	}
	if c := &codes[0]; c.Tag == 'e' {
		c.I1, c.J1 = max(c.I1, c.I2-n), max(c.J1, c.J2-n)
	}
	if c := &codes[len(codes)-1]; c.Tag == 'e' {
		c.I2, c.J2 = min(c.I2, c.I1+n), min(c.J2, c.J1+n)
	}
	var groups [][]op
	var group []op
	for _, c := range codes {
		if c.Tag == 'e' && c.I2-c.I1 > 2*n {
			group = append(group, op{'e', c.I1, min(c.I2, c.I1+n), c.J1, min(c.J2, c.J1+n)})
			groups = append(groups, group)
			group = nil
			c.I1, c.J1 = max(c.I1, c.I2-n), max(c.J1, c.J2-n)
		}
		group = append(group, c)
	}
	if len(group) > 0 && !(len(group) == 1 && group[0].Tag == 'e') {
		groups = append(groups, group)
	}
	return groups
}
