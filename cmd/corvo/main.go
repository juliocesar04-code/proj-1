// Command corvo is the shell for a CorvoDB database file.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/juliocesar04-code/proj-1/internal/engine"
	"github.com/juliocesar04-code/proj-1/internal/types"
)

const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "corvo:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		command     = flag.String("c", "", "run a statement and exit")
		file        = flag.String("f", "", "run the statements in a file and exit")
		readOnly    = flag.Bool("readonly", false, "open the database without allowing writes")
		noSync      = flag.Bool("nosync", false, "skip the fsync on commit (faster, less durable)")
		cacheSize   = flag.Int("cache", 1024, "number of pages kept in the buffer pool")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("corvo", version)
		return nil
	}

	if flag.NArg() > 1 {
		return fmt.Errorf("unexpected argument %q: flags have to come before the database path", flag.Arg(1))
	}
	path := flag.Arg(0)
	if path == "" {
		path = "corvo.db"
	}

	db, err := engine.Open(path, engine.Options{
		CacheSize: *cacheSize,
		NoSync:    *noSync,
		ReadOnly:  *readOnly,
	})
	if err != nil {
		return err
	}
	defer db.Close()

	shell := &shell{db: db, session: db.Session(), out: os.Stdout}
	defer shell.session.Close()

	switch {
	case *command != "":
		return shell.execute(*command)
	case *file != "":
		source, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		return shell.execute(string(source))
	default:
		return shell.repl(os.Stdin)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `corvo %s - shell for CorvoDB

usage: corvo [flags] [database]

The database defaults to corvo.db in the current directory. Without -c or -f
the shell reads statements from standard input.

flags:
`, version)
	flag.PrintDefaults()
}

type shell struct {
	db      *engine.DB
	session *engine.Session
	out     io.Writer
	timing  bool
}

func (s *shell) repl(input io.Reader) error {
	reader := bufio.NewReader(input)
	interactive := isTerminal(input)
	if interactive {
		fmt.Fprintf(s.out, "corvo %s, banco %s\nDigite .help para os comandos do shell.\n\n", version, s.db.Path())
	}

	var buffer strings.Builder
	for {
		if interactive {
			fmt.Fprint(s.out, s.prompt(buffer.Len() > 0))
		}
		line, err := reader.ReadString('\n')
		if line == "" && errors.Is(err, io.EOF) {
			if interactive {
				fmt.Fprintln(s.out)
			}
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		trimmed := strings.TrimSpace(line)
		if buffer.Len() == 0 {
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, ".") {
				stop, err := s.dotCommand(trimmed)
				if err != nil {
					fmt.Fprintln(s.out, "error:", err)
				}
				if stop {
					return nil
				}
				continue
			}
		}

		buffer.WriteString(line)
		if !strings.HasSuffix(trimmed, ";") {
			continue
		}
		if err := s.execute(buffer.String()); err != nil {
			fmt.Fprintln(s.out, "error:", err)
		}
		buffer.Reset()
	}
}

func (s *shell) prompt(continuation bool) string {
	if continuation {
		return "   ...> "
	}
	if s.session.InTransaction() {
		return "corvo*> "
	}
	return "corvo> "
}

func (s *shell) execute(query string) error {
	start := time.Now()
	results, err := s.session.Exec(query)
	elapsed := time.Since(start)

	for _, result := range results {
		s.print(result)
	}
	if err != nil {
		return err
	}
	if s.timing {
		fmt.Fprintf(s.out, "time: %s\n", elapsed.Round(time.Microsecond))
	}
	return nil
}

func (s *shell) print(result engine.Result) {
	if len(result.Columns) == 0 {
		switch result.Tag {
		case "INSERT", "UPDATE", "DELETE":
			fmt.Fprintf(s.out, "%s %d\n", result.Tag, result.RowsAffected)
		case "":
		default:
			fmt.Fprintln(s.out, result.Tag)
		}
		return
	}

	cells := make([][]string, 0, len(result.Rows))
	numeric := make([]bool, len(result.Columns))
	for _, row := range result.Rows {
		line := make([]string, len(row))
		for i, value := range row {
			line[i] = value.String()
			if i < len(numeric) && (value.T == types.Integer || value.T == types.Float) {
				numeric[i] = true
			}
		}
		cells = append(cells, line)
	}

	widths := make([]int, len(result.Columns))
	for i, name := range result.Columns {
		widths[i] = len([]rune(name))
	}
	for _, line := range cells {
		for i, text := range line {
			if width := len([]rune(text)); width > widths[i] {
				widths[i] = width
			}
		}
	}

	header := make([]string, len(result.Columns))
	rule := make([]string, len(result.Columns))
	for i, name := range result.Columns {
		header[i] = pad(name, widths[i], false)
		rule[i] = strings.Repeat("-", widths[i]+2)
	}
	fmt.Fprintf(s.out, " %s \n", strings.Join(header, " | "))
	fmt.Fprintln(s.out, strings.Join(rule, "+"))

	for _, line := range cells {
		padded := make([]string, len(line))
		for i, text := range line {
			padded[i] = pad(text, widths[i], numeric[i])
		}
		fmt.Fprintf(s.out, " %s \n", strings.Join(padded, " | "))
	}

	label := "rows"
	if len(cells) == 1 {
		label = "row"
	}
	fmt.Fprintf(s.out, "(%d %s)\n", len(cells), label)
}

// pad aligns a cell: numbers to the right, everything else to the left.
func pad(text string, width int, right bool) string {
	filler := strings.Repeat(" ", width-len([]rune(text)))
	if right {
		return filler + text
	}
	return text + filler
}

func (s *shell) dotCommand(line string) (stop bool, err error) {
	fields := strings.Fields(line)
	switch fields[0] {
	case ".exit", ".quit":
		return true, nil

	case ".help":
		fmt.Fprint(s.out, helpText)

	case ".tables":
		for _, name := range s.db.Catalog().TableNames() {
			fmt.Fprintln(s.out, name)
		}

	case ".schema":
		catalog := s.db.Catalog()
		names := catalog.TableNames()
		if len(fields) > 1 {
			names = fields[1:]
		}
		for _, name := range names {
			table, ok := catalog.Table(name)
			if !ok {
				return false, fmt.Errorf("no such table: %s", name)
			}
			fmt.Fprint(s.out, describe(table))
		}

	case ".indexes":
		catalog := s.db.Catalog()
		for _, name := range catalog.TableNames() {
			table, _ := catalog.Table(name)
			for _, index := range table.Indexes {
				unique := ""
				if index.Unique {
					unique = " unique"
				}
				fmt.Fprintf(s.out, "%s on %s(%s)%s\n", index.Name, table.Name, index.Column, unique)
			}
		}

	case ".timer":
		if len(fields) > 1 {
			s.timing = fields[1] == "on"
		} else {
			s.timing = !s.timing
		}
		fmt.Fprintf(s.out, "timer %s\n", onOff(s.timing))

	default:
		return false, fmt.Errorf("unknown command %s, try .help", fields[0])
	}
	return false, nil
}

func describe(table *engine.Table) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE TABLE %s (\n", table.Name)
	for i, column := range table.Columns {
		fmt.Fprintf(&sb, "    %s %s", column.Name, column.Type)
		if column.PrimaryKey {
			sb.WriteString(" PRIMARY KEY")
		} else {
			if column.NotNull {
				sb.WriteString(" NOT NULL")
			}
			if column.Unique {
				sb.WriteString(" UNIQUE")
			}
		}
		if i < len(table.Columns)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(");\n")
	return sb.String()
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func isTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

const helpText = `.help            show this help
.tables          list the tables
.schema [table]  print the definition of a table, or of every table
.indexes         list the indexes
.timer [on|off]  show how long each statement takes
.exit            leave the shell

Statements end with a semicolon and may span several lines.
`
