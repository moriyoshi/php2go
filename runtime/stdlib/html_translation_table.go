package stdlib

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

//go:generate go run ./cmd/entitygen -output html_translation_tables.bin

//go:embed html_translation_tables.bin
var htmlTranslationTableData []byte

const htmlTranslationTableMagic = "P2GETBL1"

type htmlTranslationEntry struct {
	key   string
	value string
}

var htmlTranslationTables = mustDecodeHTMLTranslationTables(htmlTranslationTableData)

func mustDecodeHTMLTranslationTables(data []byte) map[string][]htmlTranslationEntry {
	tables, err := decodeHTMLTranslationTables(data)
	if err != nil {
		panic(err)
	}
	return tables
}

func decodeHTMLTranslationTables(data []byte) (map[string][]htmlTranslationEntry, error) {
	if len(data) < len(htmlTranslationTableMagic)+4 ||
		string(data[:len(htmlTranslationTableMagic)]) != htmlTranslationTableMagic {
		return nil, fmt.Errorf("php2go: invalid embedded HTML translation table header")
	}
	data = data[len(htmlTranslationTableMagic):]
	datasetCount, data, ok := readHTMLTableUint32(data)
	if !ok {
		return nil, fmt.Errorf("php2go: truncated embedded HTML translation table count")
	}
	tables := make(map[string][]htmlTranslationEntry, datasetCount)
	for dataset := uint32(0); dataset < datasetCount; dataset++ {
		id, rest, ok := readHTMLTableString(data)
		if !ok {
			return nil, fmt.Errorf("php2go: truncated embedded HTML translation table id")
		}
		data = rest
		entryCount, rest, ok := readHTMLTableUint32(data)
		if !ok {
			return nil, fmt.Errorf("php2go: truncated embedded HTML translation table size")
		}
		data = rest
		entries := make([]htmlTranslationEntry, 0, entryCount)
		for entry := uint32(0); entry < entryCount; entry++ {
			key, rest, ok := readHTMLTableString(data)
			if !ok {
				return nil, fmt.Errorf("php2go: truncated embedded HTML translation table key")
			}
			data = rest
			value, rest, ok := readHTMLTableString(data)
			if !ok {
				return nil, fmt.Errorf("php2go: truncated embedded HTML translation table value")
			}
			data = rest
			entries = append(entries, htmlTranslationEntry{key: key, value: value})
		}
		if _, duplicate := tables[id]; duplicate {
			return nil, fmt.Errorf("php2go: duplicate embedded HTML translation table %q", id)
		}
		tables[id] = entries
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("php2go: trailing embedded HTML translation table data")
	}
	return tables, nil
}

func readHTMLTableUint32(data []byte) (uint32, []byte, bool) {
	if len(data) < 4 {
		return 0, data, false
	}
	return binary.BigEndian.Uint32(data), data[4:], true
}

func readHTMLTableString(data []byte) (string, []byte, bool) {
	size, rest, ok := readHTMLTableUint32(data)
	if !ok || uint64(size) > uint64(len(rest)) {
		return "", data, false
	}
	return string(rest[:size]), rest[size:], true
}

var htmlTranslationEncodingAliases = map[string]string{
	"utf-8":        "utf-8",
	"iso-8859-1":   "iso-8859-1",
	"iso8859-1":    "iso-8859-1",
	"iso-8859-15":  "iso-8859-15",
	"iso8859-15":   "iso-8859-15",
	"cp1252":       "cp1252",
	"windows-1252": "cp1252",
	"1252":         "cp1252",
	"cp1251":       "cp1251",
	"windows-1251": "cp1251",
	"win-1251":     "cp1251",
	"iso8859-5":    "iso-8859-5",
	"iso-8859-5":   "iso-8859-5",
	"cp866":        "cp866",
	"866":          "cp866",
	"ibm866":       "cp866",
	"macroman":     "macroman",
	"koi8-r":       "koi8-r",
	"koi8-ru":      "koi8-r",
	"koi8r":        "koi8-r",
}

var htmlTranslationPartialEncodings = map[string]bool{
	"big5": true, "950": true,
	"gb2312": true, "936": true,
	"big5-hkscs": true,
	"shift_jis":  true, "sjis": true, "932": true,
	"sjis-win": true, "cp932": true,
	"eucjp": true, "euc-jp": true, "eucjp-win": true,
}

func internalHTMLTranslationTable(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	table := int64(0)
	flags := int64(entDefaultFlags)
	encoding := "UTF-8"
	if len(args) > 0 {
		table = args[0].Deref().Int()
	}
	if len(args) > 1 {
		flags = args[1].Deref().Int()
	}
	if len(args) > 2 {
		encoding = args[2].Deref().String()
	}
	if nul := strings.IndexByte(encoding, 0); nul >= 0 {
		encoding = encoding[:nul]
	}
	lower := strings.ToLower(encoding)
	canonical, known := htmlTranslationEncodingAliases[lower]
	partial := htmlTranslationPartialEncodings[lower]
	if !known && !partial {
		ctx.Warn("get_html_translation_table(): Charset \"%s\" is not supported, assuming UTF-8", encoding)
		canonical = "utf-8"
	}
	doctype := flags & entDoctypeMask
	all := table != 0
	if doctype == entDoctypeXML1 || partial {
		all = false
		canonical = "utf-8"
	}
	id := fmt.Sprintf("%s|%d|%d", canonical, doctype, boolInt(all))
	entries, found := htmlTranslationTables[id]
	if !found {
		return php.Undef(), fmt.Errorf("php2go: missing embedded HTML translation table %q", id)
	}
	result := php.NewArray()
	for _, entry := range entries {
		if entry.key == "'" && flags&entQuoteSingle == 0 {
			continue
		}
		if entry.key == "\"" && flags&entQuoteDouble == 0 {
			continue
		}
		result.Set(php.StringKey(entry.key), php.String(entry.value))
	}
	return php.Arr(result), nil
}

// boolInt is the int a PHP boolean carries as, which the table lookup needs to
// index the quote-style variants.
func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func init() {
	// The helper the userland `get_html_translation_table` calls. It moved here
	// with the table it reads; the interpreter reached it through a map entry
	// in `builtins.go`, which is the registration this replaces.
	php.RegisterBuiltin(`php2go\internal\html_translation_table`, internalHTMLTranslationTable)
}
