package interp

import (
	"fmt"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
)

func networkDatabaseBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if !libc.Available() {
		return php.Undef(), &UnsupportedError{
			Opcode: "libc network databases without cgo",
		}
	}
	if len(args) < 2 {
		return php.Undef(), fmt.Errorf(
			"php2go internal network_database requires an operation and value")
	}
	operation := args[0].Deref().String()
	value := args[1].Deref()
	switch operation {
	case "host-by-name":
		result, ok := libc.HostByName(value.String())
		if !ok {
			return php.Bool(false), nil
		}
		return php.String(result), nil
	case "host-by-name-list":
		results, ok := libc.HostByNameList(value.String())
		if !ok {
			return php.Bool(false), nil
		}
		array := php.NewArray()
		for _, result := range results {
			array.Append(php.String(result))
		}
		return php.Arr(array), nil
	case "host-by-address":
		result, ok := libc.HostByAddress(value.String())
		if !ok {
			return php.Bool(false), nil
		}
		return php.String(result), nil
	case "protocol-by-name":
		result, ok := libc.ProtocolByName(value.String())
		if !ok {
			return php.Bool(false), nil
		}
		return php.Int(result), nil
	case "protocol-by-number":
		result, ok := libc.ProtocolByNumber(value.Int())
		if !ok {
			return php.Bool(false), nil
		}
		return php.String(result), nil
	case "service-by-name":
		if len(args) < 3 {
			return php.Undef(), fmt.Errorf(
				"php2go internal network_database service lookup requires a protocol")
		}
		result, ok := libc.ServiceByName(
			value.String(), args[2].Deref().String())
		if !ok {
			return php.Bool(false), nil
		}
		return php.Int(result), nil
	case "service-by-port":
		if len(args) < 3 {
			return php.Undef(), fmt.Errorf(
				"php2go internal network_database service lookup requires a protocol")
		}
		result, ok := libc.ServiceByPort(
			value.Int(), args[2].Deref().String())
		if !ok {
			return php.Bool(false), nil
		}
		return php.String(result), nil
	case "interfaces":
		entries, errorNumber := libc.Interfaces()
		if errorNumber != 0 {
			return php.String(fmt.Sprintf(
				"getifaddrs() failed %d: %s",
				errorNumber, syscall.Errno(errorNumber))), nil
		}
		result := php.NewArray()
		type interfaceArrays struct {
			value   *php.Array
			unicast *php.Array
		}
		interfaces := make(map[string]interfaceArrays)
		for _, entry := range entries {
			arrays, found := interfaces[entry.Name]
			if !found {
				arrays = interfaceArrays{
					value:   php.NewArray(),
					unicast: php.NewArray(),
				}
				arrays.value.Set(
					php.StringKey("unicast"), php.Arr(arrays.unicast))
				result.Set(php.StringKey(entry.Name), php.Arr(arrays.value))
				interfaces[entry.Name] = arrays
			}
			address := php.NewArray()
			address.Set(php.StringKey("flags"), php.Int(int64(entry.Flags)))
			if entry.HasFamily {
				address.Set(
					php.StringKey("family"), php.Int(int64(entry.Family)))
			}
			if entry.Address != "" {
				address.Set(php.StringKey("address"), php.String(entry.Address))
			}
			if entry.Netmask != "" {
				address.Set(php.StringKey("netmask"), php.String(entry.Netmask))
			}
			if entry.Broadcast != "" {
				address.Set(
					php.StringKey("broadcast"), php.String(entry.Broadcast))
			}
			if entry.PointToPoint != "" {
				address.Set(
					php.StringKey("ptp"), php.String(entry.PointToPoint))
			}
			arrays.unicast.Append(php.Arr(address))
			if !found {
				arrays.value.Set(php.StringKey("up"), php.Bool(entry.Up))
			}
		}
		return php.Arr(result), nil
	default:
		return php.Undef(), fmt.Errorf(
			"php2go internal network_database received unknown operation %q",
			operation)
	}
}
