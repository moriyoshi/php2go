//go:build cgo && !windows

package libc

/*
#define _XOPEN_SOURCE 700
#include <langinfo.h>
#include <locale.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct {
	int tm_sec;
	int tm_min;
	int tm_hour;
	int tm_mday;
	int tm_mon;
	int tm_year;
	int tm_wday;
	int tm_yday;
} php2go_parsed_time;

static const char *php2go_strptime(
	const char *timestamp,
	const char *format,
	php2go_parsed_time *result
) {
	struct tm parsed;
	memset(&parsed, 0, sizeof(parsed));
	const char *unparsed = strptime(timestamp, format, &parsed);
	if (unparsed == NULL) {
		return NULL;
	}
	result->tm_sec = parsed.tm_sec;
	result->tm_min = parsed.tm_min;
	result->tm_hour = parsed.tm_hour;
	result->tm_mday = parsed.tm_mday;
	result->tm_mon = parsed.tm_mon;
	result->tm_year = parsed.tm_year;
	result->tm_wday = parsed.tm_wday;
	result->tm_yday = parsed.tm_yday;
	return unparsed;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

// ParsedTime is the initialized part of libc's struct tm plus the input suffix
// strptime did not consume. PHP exposes these fields without normalization.
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

// LocaleConvention is a copied snapshot of libc's struct lconv. Pointer-backed
// fields are converted while localeMu is held so a concurrent setlocale cannot
// invalidate or replace them.
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

// localeMu serializes libc operations that read the process-global locale.
// A future setlocale implementation must use this same lock and boundary.
var localeMu sync.Mutex

func init() {
	// The linked embed SAPI initializes LC_CTYPE from the environment while
	// leaving the other categories in C. A generated runtime has no Zend startup
	// to do that for it, so reproduce the same libc state here.
	empty := C.CString("")
	defer C.free(unsafe.Pointer(empty))
	localeMu.Lock()
	C.setlocale(C.LC_CTYPE, empty)
	localeMu.Unlock()
}

// SetLocale queries or mutates libc's process-global locale. The caller owns
// PHP's candidate ordering and uses query only for the exact string "0".
func SetLocale(category int, locale string, query bool) (result string, ok, available bool) {
	localeMu.Lock()
	defer localeMu.Unlock()

	var clocale *C.char
	if !query {
		clocale = C.CString(locale)
		defer C.free(unsafe.Pointer(clocale))
	}
	changed := C.setlocale(C.int(category), clocale)
	if changed == nil {
		return "", false, true
	}
	return C.GoString(changed), true, true
}

func localeGrouping(value *C.char) []int {
	if value == nil {
		return nil
	}
	length := int(C.strlen(value))
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(value)), length)
	result := make([]int, length)
	for index, entry := range bytes {
		result[index] = int(entry)
	}
	return result
}

func localeString(value *C.char) string {
	if value == nil {
		return ""
	}
	return C.GoString(value)
}

// LocaleConv copies localeconv's pointer-backed result under the shared locale
// lock. Numeric char fields are exposed as unsigned bytes, matching PHP's
// observable 255 sentinel for the C locale.
func LocaleConv() (LocaleConvention, bool) {
	localeMu.Lock()
	defer localeMu.Unlock()

	current := C.localeconv()
	if current == nil {
		return LocaleConvention{}, true
	}
	return LocaleConvention{
		DecimalPoint:    localeString(current.decimal_point),
		ThousandsSep:    localeString(current.thousands_sep),
		IntCurrSymbol:   localeString(current.int_curr_symbol),
		CurrencySymbol:  localeString(current.currency_symbol),
		MonDecimalPoint: localeString(current.mon_decimal_point),
		MonThousandsSep: localeString(current.mon_thousands_sep),
		PositiveSign:    localeString(current.positive_sign),
		NegativeSign:    localeString(current.negative_sign),
		IntFracDigits:   int(uint8(current.int_frac_digits)),
		FracDigits:      int(uint8(current.frac_digits)),
		PCSPrecedes:     int(uint8(current.p_cs_precedes)),
		PSepBySpace:     int(uint8(current.p_sep_by_space)),
		NCSPrecedes:     int(uint8(current.n_cs_precedes)),
		NSepBySpace:     int(uint8(current.n_sep_by_space)),
		PSignPosn:       int(uint8(current.p_sign_posn)),
		NSignPosn:       int(uint8(current.n_sign_posn)),
		Grouping:        localeGrouping(current.grouping),
		MonGrouping:     localeGrouping(current.mon_grouping),
	}, true
}

// NlLangInfo copies one nl_langinfo result under the shared locale lock. The
// caller validates the finite item registry before entering libc.
func NlLangInfo(item int) (string, bool) {
	localeMu.Lock()
	defer localeMu.Unlock()
	value := C.nl_langinfo(C.nl_item(item))
	if value == nil {
		return "", true
	}
	return C.GoString(value), true
}

// Strptime calls the platform parser under a mutex because it reads libc's
// process-global locale. The caller distinguishes a parse failure with ok.
func Strptime(timestamp, format string) (parsed ParsedTime, ok bool) {
	localeMu.Lock()
	defer localeMu.Unlock()

	ctimestamp := C.CString(timestamp)
	defer C.free(unsafe.Pointer(ctimestamp))
	cformat := C.CString(format)
	defer C.free(unsafe.Pointer(cformat))

	var result C.php2go_parsed_time
	unparsed := C.php2go_strptime(ctimestamp, cformat, &result)
	if unparsed == nil {
		return ParsedTime{}, false
	}
	return ParsedTime{
		Second:   int(result.tm_sec),
		Minute:   int(result.tm_min),
		Hour:     int(result.tm_hour),
		MonthDay: int(result.tm_mday),
		Month:    int(result.tm_mon),
		Year:     int(result.tm_year),
		WeekDay:  int(result.tm_wday),
		YearDay:  int(result.tm_yday),
		Unparsed: C.GoString(unparsed),
	}, true
}

// Strcoll compares two C strings under libc's current process locale. PHP's
// ext/standard implementation passes zend string buffers straight to strcoll,
// so embedded NUL bytes deliberately terminate both operands here as well.
func Strcoll(left, right string) (result int, available bool) {
	localeMu.Lock()
	defer localeMu.Unlock()

	cleft := C.CString(left)
	defer C.free(unsafe.Pointer(cleft))
	cright := C.CString(right)
	defer C.free(unsafe.Pointer(cright))
	return int(C.strcoll(cleft, cright)), true
}
