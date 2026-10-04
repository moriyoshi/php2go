// Command entitygen asks the linked PHP engine for the exact translation
// tables that an engine-free bundle must carry.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/moriyoshi/php2go/internal/zend"
)

const magic = "P2GETBL1"

func main() {
	output := flag.String("output", "html_translation_tables.bin", "generated table path")
	flag.Parse()

	engine, err := zend.Start()
	if err != nil {
		fail(err)
	}
	defer engine.Close()

	data, err := engine.EvalAs(generatorPHP(), "php2go-entitygen")
	if err != nil {
		fail(err)
	}
	if err := validate([]byte(data)); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*output, []byte(data), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "entitygen:", err)
	os.Exit(1)
}

func generatorPHP() string {
	return `
$encodings = [
    'UTF-8', 'ISO-8859-1', 'ISO-8859-15', 'CP1252', 'CP1251',
    'ISO-8859-5', 'CP866', 'MacRoman', 'KOI8-R',
];
$doctypes = [0, ENT_XML1, ENT_XHTML, ENT_HTML5];
$datasets = count($encodings) * count($doctypes) * 2;
$pack_string = static function (string $value): string {
    return pack('N', strlen($value)) . $value;
};
$output = 'P2GETBL1' . pack('N', $datasets);
foreach ($encodings as $encoding) {
    foreach ($doctypes as $doctype) {
        foreach ([HTML_SPECIALCHARS, HTML_ENTITIES] as $table) {
            $id = strtolower($encoding) . '|' . $doctype . '|' . $table;
            $entries = get_html_translation_table(
                $table,
                ENT_QUOTES | ENT_SUBSTITUTE | $doctype,
                $encoding
            );
            $output .= $pack_string($id) . pack('N', count($entries));
            foreach ($entries as $key => $value) {
                $output .= $pack_string($key) . $pack_string($value);
            }
        }
    }
}
echo $output;
`
}

func validate(data []byte) error {
	if len(data) < len(magic)+4 || string(data[:len(magic)]) != magic {
		return fmt.Errorf("linked engine produced an invalid table header")
	}
	count := binary.BigEndian.Uint32(data[len(magic):])
	if count != 72 {
		return fmt.Errorf("linked engine produced %d datasets, want 72", count)
	}
	if strings.Contains(string(data[len(magic)+4:]), "Warning:") {
		return fmt.Errorf("linked engine emitted a warning while generating tables")
	}
	return nil
}
