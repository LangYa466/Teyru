#!/bin/sh
# Compiles and times the benchmark programs, optionally against a JVM baseline.
# Also measures the three rows of the README table that are not programs:
# startup (100 runs of a hello world), executable size, and peak RSS.
#
#   sh scripts/bench.sh            # all benchmarks, 3 runs each, best time
#   JAVA=0 sh scripts/bench.sh     # skip the JVM comparison
#   RUNS=1 sh scripts/bench.sh     # single run
set -e
cd "$(dirname "$0")/.."
# The compiler is built from the tree being measured, unless BIN names one to
# use instead (that is how an older commit's numbers are reproduced). It used
# to fall back to ./teyru when that file happened to exist, so a stale binary
# left in the working directory silently produced the whole table: the
# published size row came from a build of a completely different source tree.
BIN=${BIN:-}
BIN_TEMP=
if [ -z "$BIN" ]; then
  BIN=$(mktemp "${TMPDIR:-/tmp}/teyru-bench.XXXXXX")
  BIN_TEMP=$BIN
  go build -o "$BIN" ./cmd/teyru
fi
[ -x "$BIN" ] || { echo "bench: $BIN is not an executable" >&2; exit 1; }
RUNS=${RUNS:-3}
JAVA=${JAVA:-1}

# best: runs the command $RUNS times and prints the smallest wall time
best() {
  i=0
  out=""
  while [ "$i" -lt "$RUNS" ]; do
    start=$(date +%s.%N)
    "$@" >/dev/null 2>&1 || true
    end=$(date +%s.%N)
    out="$out$(awk -v a="$start" -v b="$end" 'BEGIN{printf "%.4f\n", b-a}')
"
    i=$((i+1))
  done
  printf '%s' "$out" | sort -g | head -1
}

# fmt_ratio: prints a/b, or "-" when either side was not measured
fmt_ratio() {
  if [ -n "$1" ] && [ -n "$2" ]; then
    awk -v a="$1" -v b="$2" 'BEGIN{ if (b>0) printf "%.2fx", a/b; else print "-" }'
  else
    printf '-'
  fi
}

# best_rss: runs the program $RUNS times and prints the smallest peak RSS in kB
# (/usr/bin/time -v is GNU time; without it the measurement is skipped)
best_rss() {
  [ -x /usr/bin/time ] || return 0
  i=0
  out=""
  while [ "$i" -lt "$RUNS" ]; do
    v=$(/usr/bin/time -v "$@" 2>&1 >/dev/null | awk -F': ' '/Maximum resident set size/ {print $2}')
    if [ -n "$v" ]; then
      out="$out$v
"
    fi
    i=$((i+1))
  done
  printf '%s' "$out" | sort -g | head -1
}

printf '%-20s %10s %10s %10s\n' program teyru java teyru-java
for f in examples/bench_*.teyru; do
  [ -f "$f" ] || continue
  name=$(basename "$f" .teyru)
  exe=/tmp/$name.teyru.exe
  $BIN build -O2 -o "$exe" "$f" >/dev/null
  t=$(best "$exe")
  j=""
  missing=""
  if [ "$JAVA" = "1" ] && command -v java >/dev/null 2>&1 && [ -f "examples/$name.java" ]; then
    javac -d /tmp "examples/$name.java" 2>/dev/null || true
    if [ -f "/tmp/$name.class" ]; then
      j=$(best java -cp /tmp "$name")
    else
      # A Java file that did not produce a class named after the file: the row
      # would otherwise print "-", which reads as "not measured rather than
      # measured", and a benchmark that loses is exactly the one whose column
      # must not disappear. Say so instead.
      missing="!no-class"
    fi
  fi
  if [ -n "$missing" ]; then
    printf '%-20s %9ss %9s %10s\n' "$name" "$t" "$missing" "$missing"
    continue
  fi
  if [ -n "$j" ]; then
    ratio=$(awk -v a="$j" -v b="$t" 'BEGIN{ if (b>0) printf "%.2fx", a/b; else print "-" }')
  else
    ratio="-"
  fi
  printf '%-20s %9ss %9ss %10s\n' "$name" "$t" "${j:--}" "$ratio"
done

# The three non-program rows of the README table. The JVM side of the size row
# is the installed JDK runtime, which is a property of the measuring machine
# rather than of the program, so it is not measured here.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"; [ -z "$BIN_TEMP" ] || rm -f "$BIN_TEMP"' EXIT

cat > "$TMP/hello.teyru" <<'EOF'
class Hello {
  public static void main(String[] args) {
    System.out.println("Hello, Teyru!")
  }
}
EOF

# run100 runs the program it is given 100 times, so `best` returns the best of
# $RUNS batches of 100 runs (a single process start is too short to time).
cat > "$TMP/run100.sh" <<'EOF'
#!/bin/sh
i=0
while [ "$i" -lt 100 ]; do
  "$@" >/dev/null 2>&1
  i=$((i+1))
done
EOF
chmod +x "$TMP/run100.sh"

$BIN build -O2 -o "$TMP/hello" "$TMP/hello.teyru" >/dev/null
startup=$(best "$TMP/run100.sh" "$TMP/hello")
size=$(wc -c < "$TMP/hello" | awk '{printf "%.1f", $1/1024}')
rss=$(best_rss "$TMP/hello")

jstartup=""
jrss=""
if [ "$JAVA" = "1" ] && command -v java >/dev/null 2>&1; then
  cat > "$TMP/Hello.java" <<'EOF'
public class Hello {
  public static void main(String[] args) {
    System.out.println("Hello, Teyru!");
  }
}
EOF
  if javac -d "$TMP" "$TMP/Hello.java" 2>/dev/null; then
    jstartup=$(best "$TMP/run100.sh" java -cp "$TMP" Hello)
    jrss=$(best_rss java -cp "$TMP" Hello)
  fi
fi

jstartup_cell="-"
if [ -n "$jstartup" ]; then
  jstartup_cell="${jstartup}s"
fi
jrss_cell="-"
if [ -n "$jrss" ]; then
  jrss_cell="${jrss}kB"
fi

printf '\n'
printf '%-20s %10s %10s %10s\n' metric teyru java teyru-java
printf '%-20s %10s %10s %10s\n' startup-100x "${startup}s" "$jstartup_cell" "$(fmt_ratio "$jstartup" "$startup")"
printf '%-20s %10s %10s %10s\n' hello-size "${size}KB" - -
printf '%-20s %10s %10s %10s\n' peak-rss "${rss:-?}kB" "$jrss_cell" "$(fmt_ratio "$jrss" "$rss")"
