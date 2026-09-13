// Command teyru is the Teyru compiler: a native compiler that lowers Teyru
// source to C and links it into a standalone binary with no JVM involved.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LangYa466/Teyru/internal/driver"
)

const usage = `teyru - the Teyru compiler

usage:
  teyru build [flags] <files...>   compile to a native executable
  teyru run   [flags] <files...> [-- args...]  compile and run
  teyru emit  [flags] <files...>   print the generated C
  teyru emit-llvm [flags] <files...>  print the LLVM IR the backend feeds to LLVM
  teyru version                    print the version

flags:
  -o <path>     output executable (default a.out)
  -c <path>     keep the generated C at <path>
  --cc <name>   C compiler to use (default clang)
  -O0..-O3      optimisation level (default -O2)
  --llvm-ir <p> write the LLVM IR module to <p> (the backend is clang/LLVM)
  --no-lto      disable link-time optimisation
  --native <f>  C source implementing the program's native methods (repeatable)
  --link <arg>  extra argument for the link step, such as -lm or a .a path
  --native-header <p>  write the C prototypes of every native method to <p>
  -v            verbose
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	opts := driver.Options{Out: "a.out"}
	var files []string
	var progArgs []string
	afterSep := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case afterSep:
			progArgs = append(progArgs, a)
		case a == "--":
			afterSep = true
		case a == "-o":
			i++
			if i < len(args) {
				opts.Out = args[i]
			}
		case a == "-c":
			i++
			if i < len(args) {
				opts.CFile = args[i]
				opts.EmitC = args[i]
			}
		case a == "--cc":
			i++
			if i < len(args) {
				opts.CC = args[i]
			}
		case a == "--no-lto":
			opts.NoLTO = true
		case a == "-v":
			opts.Verbose = true
		case a == "--native":
			i++
			if i < len(args) {
				opts.Native = append(opts.Native, args[i])
			}
		case a == "--link":
			i++
			if i < len(args) {
				opts.Link = append(opts.Link, args[i])
			}
		case a == "--native-header":
			i++
			if i < len(args) {
				opts.NativeHeader = args[i]
			}
		case a == "--llvm-ir":
			i++
			if i < len(args) {
				opts.EmitLLVM = args[i]
			}
		case len(a) > 2 && a[0] == '-' && a[1] == 'O':
			opts.Opt = a
		default:
			files = append(files, a)
		}
	}

	switch cmd {
	case "version", "--version", "-V":
		fmt.Println(driver.Version())
		return
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	case "build", "run", "emit", "emit-llvm":
	default:
		fmt.Fprintf(os.Stderr, "teyru: unknown command %q\n", cmd)
		fmt.Print(usage)
		os.Exit(2)
	}

	if cmd == "run" {
		dir, err := os.MkdirTemp("", "teyru-build-")
		if err != nil {
			fail(err)
		}
		defer os.RemoveAll(dir)
		opts.Out = filepath.Join(dir, "program")
		opts.CFile = filepath.Join(dir, "program.c")
	}
	if cmd == "emit" {
		// `emit` prints the generated C: it must not compile or link anything,
		// and it must not leave a binary behind, so the C compiler is skipped.
		opts.CFile = filepath.Join(os.TempDir(), "teyru-emit.c")
		opts.EmitC = ""
		opts.CSourceOnly = true
	}
	if cmd == "emit-llvm" {
		dir, err := os.MkdirTemp("", "teyru-llvm-")
		if err != nil {
			fail(err)
		}
		defer os.RemoveAll(dir)
		opts.Out = filepath.Join(dir, "program")
		opts.CFile = filepath.Join(dir, "program.c")
		opts.EmitLLVM = filepath.Join(dir, "program.ll")
	}

	start := time.Now()
	res, err := driver.Compile(files, opts)
	if err != nil {
		if res != nil && res.Diags != nil && len(res.Diags.List) > 0 {
			fmt.Fprint(os.Stderr, res.Diags.String())
		}
		fail(err)
	}
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "teyru: compiled in %s\n", time.Since(start).Round(time.Millisecond))
	}
	switch cmd {
	case "emit":
		data, err := os.ReadFile(res.CFile)
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(data)
		os.Remove(res.CFile)
	case "emit-llvm":
		data, err := os.ReadFile(res.LLVMFile)
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(data)
	case "run":
		code, err := driver.Run(res.Exe, progArgs)
		if err != nil {
			fail(err)
		}
		os.Exit(code)
	default:
		fmt.Println(res.Exe)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "teyru: %v\n", err)
	os.Exit(1)
}
