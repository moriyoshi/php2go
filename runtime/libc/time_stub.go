//go:build !cgo || windows

package libc

// ParsedTime mirrors the libc-backed result on platforms where that capability
// is unavailable.
type ParsedTime struct {
	Second   int
	Minute   int
	Hour     int
	MonthDay int
	Month    int
	Year     int
	WeekDay  int
	YearDay  int
	Unparsed string
}

type LocaleConvention struct {
	DecimalPoint, ThousandsSep       string
	IntCurrSymbol, CurrencySymbol    string
	MonDecimalPoint, MonThousandsSep string
	PositiveSign, NegativeSign       string
	IntFracDigits, FracDigits        int
	PCSPrecedes, PSepBySpace         int
	NCSPrecedes, NSepBySpace         int
	PSignPosn, NSignPosn             int
	Grouping, MonGrouping            []int
}

func SetLocale(int, string, bool) (string, bool, bool) { return "", false, false }

func LocaleConv() (LocaleConvention, bool) { return LocaleConvention{}, false }

func NlLangInfo(int) (string, bool) { return "", false }

func Strptime(string, string) (ParsedTime, bool) { return ParsedTime{}, false }

// Strcoll reports that libc collation is unavailable without cgo or on the
// currently unsupported Windows native boundary.
func Strcoll(string, string) (int, bool) { return 0, false }
