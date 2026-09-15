package codegen

// This file prunes the vtable slots of the C the emitter writes, and it is the
// one place where the C back end decides reachability itself instead of leaving
// it to link-time optimisation.
//
// Everything else the back end writes is written whole, and that is on purpose:
// clang's LTO runs over the whole translation unit and drops a method whose body
// nothing calls. What it cannot drop is a method whose address is taken, and a
// vtable is nothing but addresses taken. `vt_X[i] = (void*)M_X_i` keeps M_X_i
// alive for as long as cls_X is, and a class is alive in every program: main
// installs TY_STRING, TY_OBJECT, TY_ARRAY, the boxing classes and the exception
// classes, and a live method names the classes it allocates. One live class
// therefore drags in every instance method it declares, each of those drags in
// the classes it names, and so on. A hello world that prints one string kept
// java.util.stream, because `String.lines()` sits in String's table next to
// `String.length()` and nothing can tell the two apart once both are addresses
// in a live array. Measured on this tree: the same hello world is 508,304 bytes
// with every slot filled and 56,392 bytes with every slot null, so essentially
// all of it is the standard library arriving through slots no program could
// dispatch.
//
// A dispatch reads slot `idx(selector)` of the receiver's class, and only a call
// site that dispatches that selector reads it, so a slot may be dropped exactly
// when no method the program can reach dispatches its index. That is the rule
// the LLVM back end already states for its own tables ("a vtable slot because a
// call site dispatches through it", llvm.go), and it cannot be applied per call
// site here because this back end does not know which methods LTO will keep. It
// is applied as a fixpoint over the emitted C instead:
//
//   - a live definition makes live what it names: the classes it mentions and
//     the methods it calls, and the slot index of every dispatch it makes;
//   - a live class answers slots 0, 1 and 2 for whatever object it can have,
//     because the runtime calls those three through the vtable by index
//     (print_uncaught, ty_obj_hash and ty_obj_equal in tyrt);
//   - a class answers another slot when a live dispatch asks for that index AND
//     the class can be its receiver: it must inherit from the class the dispatch
//     was compiled against. The generated C carries that class -- indirect()
//     writes `((RET(*)(OWNER*, ...))((recv)->obj.cls->vtable[N]))` -- so the test
//     is a walk over the class records, the one llvm.go states as isSubclass.
//     Without it one reachable `Class.toString` keeps slot 7 of every class that
//     overrides that selector, which is most of the library;
//   - every answer's method is live by adoption, which is what makes this a
//     fixpoint rather than one pass;
//   - the interface table is left alone. A native method a program supplies may
//     dispatch through it with a selector the compiler never sees -- that is
//     what `--native-header` exists for -- so every entry stays a plain
//     reference and every implementation it names stays live.
//
// Reading the rule off the emitted C rather than off the tree is deliberate: the
// C is what the linker sees, every dispatch is spelled out in it as an index,
// and a construct the emitter grows later is covered by the scan without anyone
// having to remember to hook it. The scan is a superset of what the program can
// run -- a slot that no live dispatch asks for is the only thing dropped -- so
// being over-inclusive costs bytes, never correctness.
//
// The rewrite is conservative in every other direction too. It only ever
// replaces a slot's initializer with NULL: it never renumbers a table or
// shortens one, so the tyclass layout the runtime and the class records agree on
// is untouched; and if the text is not shaped the way the scan expects, the
// source is returned unchanged, so a formatting change in the emitter can only
// cost the saving and never the build.

import (
	"strconv"
	"strings"
)

// objectProtocolSlots is how many leading slots every class answers for. The
// runtime reads them by index on objects it did not create: slot 0 is toString
// (print_uncaught, ty_str_concat), 1 is hashCode (ty_obj_hash) and 2 is equals
// (ty_obj_equal). tyrt.c says the same where its built-in array class mirrors
// the generated one slot for slot.
const objectProtocolSlots = 3

// dispatchPrefix is how a virtual call reads a slot: the generated C spells it
// `receiver->obj.cls->vtable[N]` (and `((tyobj*)x)->cls->vtable[N]` where the
// receiver is untyped), so the slot a dispatch needs is the integer after this.
const dispatchPrefix = "->vtable["

// pruneVtables returns src with every vtable slot that no reachable dispatch can
// read set to NULL. It returns src unchanged when the C is not shaped the way
// the scan expects.
func pruneVtables(src string) string {
	p := parseC(src)
	if p == nil {
		return src
	}
	p.liveSlots()
	return p.rewrite()
}

// cdef is one file-scope definition in the generated C: a function, or one of
// the arrays and variables a class's tables are made of.
type cdef struct {
	name  string
	fn    bool
	start int // first line of the definition
	end   int // last line, inclusive
	// refs are the definitions this one names, by index into the parse's defs.
	refs map[int]bool
	// slots are the dispatches this definition makes: the slot index read, and
	// the class the call was compiled against, whose subclasses can be its
	// receiver. Only a function has them.
	slots map[slotKey]bool
	// entries and targets are a vtable's initializer, in slot order: the text as
	// written, and the C name it holds, or "" for an entry that is already NULL.
	entries []string
	targets []string
}

// slotKey is one vtable dispatch: the index read, and the class it was compiled
// against. An empty owner means the scan could not read one, and then every class
// answers, which is the direction that cannot break a program.
type slotKey struct {
	owner string
	idx   int
}

type cparse struct {
	lines []string
	defs  []*cdef
	index map[string]int
	// roots is the entry point's references: the seed of the fixpoint. It is not
	// one of the defs, because main is written without `static`.
	roots *cdef
	// live marks the definitions the fixpoint reached, and need the dispatches a
	// reached method makes.
	live []bool
	need map[slotKey]bool
	// bases is each class's transitive supertypes, by mangled name, read out of
	// the class records the C writes: `.super = &cls_Y` and `.ifaces = if_X`.
	bases map[string]map[string]bool
}

// parseC reads the generated C into definitions. It is a line scan and not a
// parser: the emitter's output is regular -- a definition starts at column zero,
// a function ends with a line holding one closing brace, a table ends with a
// semicolon -- and all this has to get right is which definition each line
// belongs to.
//
// A definition written inside another one, such as `forname_all`, the class
// table Class.forName searches, is deliberately not a definition of its own: it
// sits in the body of the function that searches it, and the fixpoint has to see
// its references as that function's, which is exactly why it is written there.
func parseC(src string) *cparse {
	lines := strings.Split(src, "\n")
	p := &cparse{lines: lines, index: map[string]int{}}
	owned := make([]int, len(lines))
	for i := range owned {
		owned[i] = -1
	}
	mainAt := -1
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if mainAt < 0 && strings.HasPrefix(l, "int main(") {
			mainAt = i
		}
		if !strings.HasPrefix(l, "static ") {
			continue
		}
		name, sep, ok := declName(l)
		if !ok {
			continue
		}
		// A prototype names a definition written elsewhere; it has no body and
		// belongs to nothing.
		if sep == '(' && strings.HasSuffix(strings.TrimRight(l, " \t"), ");") {
			continue
		}
		d := &cdef{name: name, fn: sep == '(', start: i, end: i,
			refs: map[int]bool{}, slots: map[slotKey]bool{}}
		end, ok := defEnd(lines, i, d.fn)
		if !ok {
			return nil // a body or a table that does not end: not this shape
		}
		d.end = end
		if !d.fn && strings.HasPrefix(name, "vt_") {
			d.entries, d.targets = entriesOf(strings.Join(lines[d.start:d.end+1], ""))
			if len(d.entries) == 0 {
				return nil
			}
		}
		idx := len(p.defs)
		p.defs = append(p.defs, d)
		p.index[name] = idx
		for k := d.start; k <= d.end; k++ {
			owned[k] = idx
		}
		i = d.end
	}
	if mainAt < 0 || len(p.defs) == 0 {
		return nil
	}
	p.buildBases()
	for i, d := range p.defs {
		for k := d.start; k <= d.end; k++ {
			if owned[k] == i {
				p.scanLine(lines[k], d)
			}
		}
	}
	p.roots = &cdef{name: "int main", refs: map[int]bool{}, slots: map[slotKey]bool{}}
	for k := mainAt; k < len(lines); k++ {
		p.scanLine(lines[k], p.roots)
	}
	p.live = make([]bool, len(p.defs))
	p.need = map[slotKey]bool{}
	return p
}

// buildBases reads each class's supertypes out of the class records, which is all
// a subclass test needs: `.super = &cls_Y` names the one class it extends,
// `.ifaces = if_X` names the array its interfaces are written in, and that array
// is a list of `&cls_I`. An interface is walked like a superclass, which can only
// make the test answer yes more often -- the safe direction, since a slot kept is
// a byte and a slot dropped wrongly is a jump to NULL.
func (p *cparse) buildBases() {
	p.bases = map[string]map[string]bool{}
	parents := map[string][]string{}
	for _, d := range p.defs {
		if d.fn || !strings.HasPrefix(d.name, "cls_") {
			continue
		}
		me := d.name[len("cls_"):]
		body := strings.Join(p.lines[d.start:d.end+1], " ")
		if sup := recordField(body, "super"); strings.HasPrefix(sup, "&cls_") {
			parents[me] = append(parents[me], sup[len("&cls_"):])
		}
		arr := recordField(body, "ifaces")
		if !strings.HasPrefix(arr, "if_") {
			continue
		}
		def, ok := p.lookup(arr)
		if !ok {
			continue
		}
		parents[me] = append(parents[me], classRefs(p.lines[def.start])...)
	}
	var walk func(string, map[string]bool) map[string]bool
	walk = func(c string, onPath map[string]bool) map[string]bool {
		if b, ok := p.bases[c]; ok {
			return b
		}
		if onPath[c] {
			return map[string]bool{}
		}
		onPath[c] = true
		b := map[string]bool{}
		for _, parent := range parents[c] {
			b[parent] = true
			for up := range walk(parent, onPath) {
				b[up] = true
			}
		}
		delete(onPath, c)
		p.bases[c] = b
		return b
	}
	for c := range parents {
		walk(c, map[string]bool{})
	}
}

// recordField reads one designator out of a class record: the text after
// `.name = ` up to the comma that ends it.
func recordField(body, name string) string {
	m := strings.Index(body, "."+name+" = ")
	if m < 0 {
		return ""
	}
	rest := body[m+len(name)+4:]
	if end := strings.IndexByte(rest, ','); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// classRefs lists the classes one line names as `&cls_X`.
func classRefs(line string) []string {
	var out []string
	for k := 0; ; {
		m := strings.Index(line[k:], "&cls_")
		if m < 0 {
			return out
		}
		k += m + len("&cls_")
		end := k
		for end < len(line) && isIdentByte(line[end]) {
			end++
		}
		out = append(out, line[k:end])
		k = end
	}
}

// lookup finds a definition by name.
func (p *cparse) lookup(name string) (*cdef, bool) {
	i, ok := p.index[name]
	if !ok {
		return nil, false
	}
	return p.defs[i], true
}

// defEnd returns the last line of the definition that starts at line start: for
// a function the line holding the brace that closes its body, for a table the
// line holding the semicolon that ends it.
//
// Braces are counted rather than looked for, because a line holding one closing
// brace is not a reliable end: a pattern switch is lowered to a statement
// expression that emits its own closing brace at the left margin, inside the
// function that contains it, and treating that brace as the end of the function
// cut the body short and lost every dispatch written after it -- which is how a
// sealed interface with a `case Circle c` arm came to be pruned against a slot it
// really reads. Braces inside a string literal or a comment are not braces: the
// generated C carries JSON in its string literals, and "{}" is a string.
func defEnd(lines []string, start int, fn bool) (int, bool) {
	depth := 0
	inBlock := false // inside a /* */ comment, which may span lines
	seenBody := false
	for i := start; i < len(lines); i++ {
		l := lines[i]
		for j := 0; j < len(l); j++ {
			c := l[j]
			switch {
			case inBlock:
				if c == '*' && j+1 < len(l) && l[j+1] == '/' {
					inBlock = false
					j++
				}
			case c == '/' && j+1 < len(l) && l[j+1] == '*':
				inBlock = true
				j++
			case c == '/' && j+1 < len(l) && l[j+1] == '/':
				j = len(l) // the rest of the line is a comment
			case c == '"' || c == '\'':
				quote := c
				j++
				for j < len(l) {
					if l[j] == '\\' {
						j += 2
						continue
					}
					if l[j] == quote {
						break
					}
					j++
				}
			case c == '{':
				depth++
				seenBody = true
			case c == '}':
				depth--
				if fn && depth == 0 {
					return i, true
				}
			case c == ';' && depth == 0:
				if !fn {
					return i, true
				}
			}
		}
	}
	_ = seenBody
	return 0, false
}

// declName returns the name a file-scope definition declares, and the separator
// that follows it: '(' for a function, '[' for a table, '=' for a variable.
func declName(l string) (string, byte, bool) {
	rest := l[len("static "):]
	sep := strings.IndexAny(rest, "([=")
	if sep < 0 {
		return "", 0, false
	}
	end := sep
	for end > 0 && (rest[end-1] == ' ' || rest[end-1] == '\t' || rest[end-1] == '*') {
		end--
	}
	start := end
	for start > 0 && isIdentByte(rest[start-1]) {
		start--
	}
	if start == end {
		return "", 0, false
	}
	return rest[start:end], rest[sep], true
}

// entriesOf splits a table's initializer into its entries, and reports the C
// name each one holds.
func entriesOf(body string) ([]string, []string) {
	open := strings.IndexByte(body, '{')
	closeAt := strings.LastIndexByte(body, '}')
	if open < 0 || closeAt < open {
		return nil, nil
	}
	inner := strings.TrimSpace(body[open+1 : closeAt])
	if inner == "" {
		return nil, nil
	}
	entries := strings.Split(inner, ", ")
	targets := make([]string, len(entries))
	for i, e := range entries {
		t := strings.TrimPrefix(strings.TrimSpace(e), "(void*)")
		if t == "NULL" {
			t = ""
		}
		targets[i] = t
	}
	return entries, targets
}

// scanLine records what one line of a definition names: the definitions it
// mentions, and the slot indices it dispatches through.
func (p *cparse) scanLine(line string, d *cdef) {
	for i := 0; i < len(line); {
		if line[i] == '-' && strings.HasPrefix(line[i:], dispatchPrefix) {
			if n, ok := scanInt(line, i+len(dispatchPrefix), ']'); ok {
				d.slots[slotKey{owner: dispatchOwner(line, i), idx: n}] = true
			}
		}
		if !isIdentByte(line[i]) {
			i++
			continue
		}
		start := i
		for i < len(line) && isIdentByte(line[i]) {
			i++
		}
		if idx, ok := p.index[line[start:i]]; ok && p.defs[idx] != d {
			d.refs[idx] = true
		}
	}
}

// dispatchOwner reads the class a dispatch was compiled against, which is the
// class whose subclasses can be its receiver. indirect() writes a virtual call as
//
//	(( RET(*)(OWNER*, ...))((recv)->obj.cls->vtable[N]))((OWNER*)recv, ...)
//
// so OWNER is the first parameter of the function-pointer signature standing
// immediately before the lookup, and the last such signature before the lookup is
// the one that belongs to it: a receiver expression written earlier brings its own
// signature, which is nearer, and an argument written later cannot be nearer at
// all. The untyped form the synthesizer writes for hashCode,
// `((tyobj*)x)->cls->vtable[1]`, reads `void` there instead, and an owner that is
// not a class means every class -- the answer that keeps the slot.
func dispatchOwner(line string, at int) string {
	open := strings.LastIndex(line[:at], "(*)(C_")
	if open < 0 {
		return ""
	}
	rest := line[open+len("(*)(C_"):]
	end := 0
	for end < len(rest) && isIdentByte(rest[end]) {
		end++
	}
	if end == 0 || !strings.HasPrefix(rest[end:], "*") {
		return "" // a value parameter rather than the receiver: nothing to read
	}
	return rest[:end]
}

// scanInt reads the decimal integer at i, which must be followed by the given
// closing byte.
func scanInt(line string, i int, close byte) (int, bool) {
	start := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if start == i || i >= len(line) || line[i] != close {
		return 0, false
	}
	n, err := strconv.Atoi(line[start:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

// liveSlots runs the fixpoint: it marks the definitions the program can reach in
// p.live and records the dispatches they make in p.need.
func (p *cparse) liveSlots() {
	queue := make([]int, 0, len(p.defs))
	enqueue := func(i int) {
		if !p.live[i] {
			p.live[i] = true
			queue = append(queue, i)
		}
	}
	for i := range p.roots.refs {
		enqueue(i)
	}
	for k := range p.roots.slots {
		p.need[k] = true
	}
	for {
		for len(queue) > 0 {
			i := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			d := p.defs[i]
			for k := range d.slots {
				p.need[k] = true
			}
			// A vtable's entries are not references. They are the slots a live
			// dispatch asks for, and the class loop below is what decides which
			// of them are adopted; following them here would put every method
			// the class declares back into the live set, which is the whole
			// thing this pass exists to avoid.
			if strings.HasPrefix(d.name, "vt_") {
				continue
			}
			for n := range d.refs {
				enqueue(n)
			}
		}
		// A live class answers the object protocol and every slot a live dispatch
		// of one of its supertypes asks for, and each answer is a method that is
		// then live too: every live class is re-answered whenever anything
		// changed.
		grew := false
		for i, d := range p.defs {
			if !p.live[i] || !strings.HasPrefix(d.name, "cls_") {
				continue
			}
			vt, ok := p.index["vt_"+d.name[len("cls_"):]]
			if !ok {
				continue
			}
			for n, target := range p.defs[vt].targets {
				if !p.answers(d.name[len("cls_"):], n) {
					continue
				}
				if idx, ok := p.index[target]; ok && !p.live[idx] {
					enqueue(idx)
					grew = true
				}
			}
		}
		if !grew && len(queue) == 0 {
			return
		}
	}
}

// answers reports whether class x fills slot idx: slots 0, 1 and 2 because the
// runtime calls those on whatever object it is handed, and any other slot because
// a live dispatch asks for that index on a class x inherits from.
func (p *cparse) answers(x string, idx int) bool {
	if idx < objectProtocolSlots {
		return true
	}
	for k := range p.need {
		if k.idx == idx && p.isSub(x, k.owner) {
			return true
		}
	}
	return false
}

// isSub reports whether class x is, or inherits from, the class a dispatch was
// compiled against. A class the walk knows nothing about answers yes: the test
// exists to drop slots, so the only answer that can break a program is a
// confident no.
func (p *cparse) isSub(x, owner string) bool {
	if owner == "" || x == owner {
		return true
	}
	base, ok := p.bases[x]
	if !ok {
		return true
	}
	return base[owner]
}

// rewrite returns src with every vtable slot no live dispatch asks for set to
// NULL. A slot at an object-protocol index is kept for every class, live or not:
// what the runtime reads on an object is not a property of the program's call
// sites.
func (p *cparse) rewrite() string {
	out := make([]string, len(p.lines))
	copy(out, p.lines)
	for _, d := range p.defs {
		if d.fn || !strings.HasPrefix(d.name, "vt_") {
			continue
		}
		kept := make([]string, len(d.entries))
		dropped := false
		for n := range d.entries {
			if p.answers(d.name[len("vt_"):], n) {
				kept[n] = d.entries[n]
				continue
			}
			kept[n] = "NULL"
			dropped = true
		}
		if !dropped {
			continue
		}
		out[d.start] = "static void* " + d.name + "[" + strconv.Itoa(len(kept)) +
			"] = {" + strings.Join(kept, ", ") + "};"
	}
	return strings.Join(out, "\n")
}
