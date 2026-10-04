// Command php2harvest compiles PHP scripts with the real Zend compiler and
// reports the resulting op_arrays. Nothing in the scripts is executed.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/moriyoshi/php2go/internal/zend"
)

func main() {
	layoutOnly := flag.Bool("layout", false, "report how the linked PHP was built and exit")
	builtins := flag.Bool("builtins", false, "list the engine's builtin function signatures and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: php2harvest [-layout] [-builtins] [file.php ...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := run(*layoutOnly, *builtins, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "php2harvest:", err)
		os.Exit(1)
	}
}

func run(layoutOnly, builtins bool, paths []string) error {
	layout := zend.DetectLayout()
	fmt.Printf("layout: znode_op=%dB zend_op=%dB abs_jmp_addr=%t abs_const_addr=%t\n",
		layout.SizeofZnodeOp, layout.SizeofZendOp,
		layout.AbsoluteJumpAddresses, layout.AbsoluteConstantAddresses)
	if layoutOnly {
		return nil
	}
	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()

	if builtins {
		return dumpBuiltins(engine)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no input files")
	}

	for _, path := range paths {
		unit, err := engine.CompileUnit(path)
		if err != nil {
			return err
		}
		dump(unit.File)
		for _, fn := range unit.Functions {
			fmt.Printf("\nfunction %s%s\n", fn.Name, signature(fn))
			dump(fn)
		}
		for _, closure := range unit.File.Closures {
			fmt.Printf("\nclosure %s%s\n", closure.Name, signature(closure))
			dump(closure)
		}
		for _, class := range unit.Classes {
			dumpClass(class)
		}
	}
	return nil
}

func signature(op *zend.OpArray) string {
	params := make([]string, 0, len(op.Args))
	for _, arg := range op.Args {
		text := arg.Type
		if arg.ByRef {
			text += " &"
		}
		if arg.Variadic {
			text += " ..."
		}
		params = append(params, strings.TrimSpace(text+" $"+arg.Name))
	}
	result := "(" + strings.Join(params, ", ") + ")"
	if op.ReturnType != "" {
		result += ": " + op.ReturnType
	}
	return result
}

func dumpClass(class *zend.Class) {
	fmt.Printf("\n%s %s", class.Kind, class.Name)
	if class.Parent != "" {
		fmt.Printf(" extends %s", class.Parent)
	}
	if len(class.Interfaces) > 0 {
		fmt.Printf(" implements %s", strings.Join(class.Interfaces, ", "))
	}
	if len(class.Traits) > 0 {
		fmt.Printf(" uses %s", strings.Join(class.Traits, ", "))
	}
	if class.EnumBackingType != "" {
		fmt.Printf(" backed by %s", class.EnumBackingType)
	}
	// Magic accessors are the reason a class may resist static typing, so they
	// are called out rather than buried.
	var magic []string
	for name, present := range map[string]bool{
		"__get": class.HasGet, "__set": class.HasSet,
		"__call": class.HasCall, "__callStatic": class.HasCallStatic,
	} {
		if present {
			magic = append(magic, name)
		}
	}
	if len(magic) > 0 {
		sort.Strings(magic)
		fmt.Printf("  [dynamic: %s]", strings.Join(magic, " "))
	}
	fmt.Println()

	for _, prop := range class.Properties {
		fmt.Printf("  %s", prop.Visibility)
		if prop.Static {
			fmt.Print(" static")
		}
		if prop.Readonly {
			fmt.Print(" readonly")
		}
		if prop.SetVisibility != "" {
			fmt.Printf(" %s(set)", prop.SetVisibility)
		}
		if prop.Virtual {
			fmt.Print(" virtual")
		}
		fmt.Printf(" %s $%s\n", prop.Type, prop.Name)
		if prop.GetHook != nil {
			fmt.Printf("  hook %s\n", prop.GetHook.Name)
			dump(prop.GetHook)
		}
		if prop.SetHook != nil {
			fmt.Printf("  hook %s\n", prop.SetHook.Name)
			dump(prop.SetHook)
		}
	}
	for _, method := range class.Methods {
		fmt.Printf("  method %s%s\n", method.Name, signature(method))
		dump(method)
	}
}

func dump(op *zend.OpArray) {
	fmt.Printf("\n%s  lines %d-%d  vars=%d temps=%d oplines=%d\n",
		op.Filename, op.LineStart, op.LineEnd, op.NumVars, op.NumTemps, len(op.Oplines))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// extended_value is a third operand under another name, and which meaning it
	// carries is per-opcode: the cast target for CAST, the binary op for
	// ASSIGN_OP, the isset/empty flag for ISSET_ISEMPTY, the loop exit for
	// FE_FETCH, the argument count for DO_FCALL. Leaving it out of the dump
	// hides the operand that decides what several opcodes even are.
	fmt.Fprintln(w, "  #\tLINE\tOPCODE\tOP1\tOP2\tRESULT\tEXT\tSMART")
	for _, line := range op.Oplines {
		fmt.Fprintf(w, "  %d\t%d\t%s\t%s:%d\t%s:%d\t%s:%d\t%d\t%s\n",
			line.Index, line.Line, line.OpcodeName,
			zend.OperandTypeName(line.Op1Type), line.Op1,
			zend.OperandTypeName(line.Op2Type), line.Op2,
			zend.OperandTypeName(line.ResultType), line.Result,
			line.Extended, line.SmartBranch)
	}
	w.Flush()

	// An IS_CONST operand is an index into this table, and some opcodes rely on
	// the entries *after* the one they name -- a namespaced call stores the name
	// as written, then the lowercased form, then the unqualified fallback, in
	// consecutive slots. Without the table those runs are invisible.
	if len(op.Literals) > 0 {
		fmt.Println("  literals:")
		for i, lit := range op.Literals {
			switch lit.Kind {
			case "string":
				fmt.Printf("    %d\t%s %q\n", i, lit.Kind, lit.Str)
			case "int":
				fmt.Printf("    %d\t%s %d\n", i, lit.Kind, lit.Int)
			case "float":
				fmt.Printf("    %d\t%s %g\n", i, lit.Kind, lit.Float)
			default:
				fmt.Printf("    %d\t%s\n", i, lit.Kind)
			}
		}
	}
}

// dumpBuiltins lists what the linked build actually provides, which is the
// specification any reimplementation has to satisfy.
func dumpBuiltins(engine *zend.Engine) error {
	functions, err := engine.Builtins()
	if err != nil {
		return err
	}
	for _, fn := range functions {
		params := make([]string, 0, len(fn.Args))
		for _, arg := range fn.Args {
			text := arg.Type
			if arg.ByRef {
				text += " &"
			}
			if arg.Variadic {
				text += " ..."
			}
			params = append(params, strings.TrimSpace(text+" $"+arg.Name))
		}
		fmt.Printf("%s(%s): %s\trequired=%d\n",
			fn.Name, strings.Join(params, ", "), fn.ReturnType, fn.RequiredArgs)
	}
	fmt.Fprintf(os.Stderr, "%d builtin functions\n", len(functions))
	return nil
}
