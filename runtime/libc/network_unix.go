//go:build cgo && !windows

// Package libc exposes the small set of process-global C library databases
// whose behavior is part of PHP's standard extension contract.
package libc

/*
#include <arpa/inet.h>
#include <errno.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>
#include <netdb.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static char *php2go_copy_string(const char *value) {
	if (value == NULL) {
		return NULL;
	}
	size_t length = strlen(value);
	char *copy = malloc(length + 1);
	if (copy == NULL) {
		return NULL;
	}
	memcpy(copy, value, length + 1);
	return copy;
}

static char *php2go_host_by_name(const char *name) {
	struct hostent *entry = gethostbyname(name);
	if (entry == NULL || entry->h_addr_list == NULL ||
	    entry->h_addr_list[0] == NULL) {
		return php2go_copy_string(name);
	}
	char address[INET_ADDRSTRLEN];
	if (inet_ntop(AF_INET, entry->h_addr_list[0], address,
	    sizeof(address)) == NULL) {
		return NULL;
	}
	return php2go_copy_string(address);
}

static char *php2go_host_by_name_list(const char *name, size_t *count) {
	*count = 0;
	struct hostent *entry = gethostbyname(name);
	if (entry == NULL || entry->h_addr_list == NULL) {
		return NULL;
	}
	size_t capacity = 0;
	while (entry->h_addr_list[capacity] != NULL) {
		capacity++;
	}
	if (capacity == 0) {
		return NULL;
	}
	char *addresses = calloc(capacity, INET_ADDRSTRLEN);
	if (addresses == NULL) {
		return NULL;
	}
	for (size_t i = 0; i < capacity; i++) {
		char *slot = addresses + (*count * INET_ADDRSTRLEN);
		if (inet_ntop(AF_INET, entry->h_addr_list[i], slot,
		    INET_ADDRSTRLEN) != NULL) {
			(*count)++;
		}
	}
	return addresses;
}

static const char *php2go_host_list_at(char *addresses, size_t index) {
	return addresses + (index * INET_ADDRSTRLEN);
}

static char *php2go_host_by_address(const char *ip) {
	struct in_addr address;
	address.s_addr = inet_addr(ip);
	if (address.s_addr == (in_addr_t) -1) {
		return NULL;
	}
	struct hostent *entry = gethostbyaddr(
		(const char *) &address, sizeof(address), AF_INET);
	if (entry == NULL || entry->h_name == NULL || entry->h_name[0] == '\0') {
		return php2go_copy_string(ip);
	}
	return php2go_copy_string(entry->h_name);
}

static int64_t php2go_protocol_by_name(const char *name, int *found) {
	struct protoent *entry = getprotobyname(name);
	if (entry == NULL) {
		*found = 0;
		return 0;
	}
	*found = 1;
	return entry->p_proto;
}

static char *php2go_protocol_by_number(int64_t number) {
	struct protoent *entry = getprotobynumber((int) number);
	return entry == NULL ? NULL : php2go_copy_string(entry->p_name);
}

static int64_t php2go_service_by_name(
	const char *name, const char *protocol, int *found
) {
	struct servent *entry = getservbyname(name, protocol);
	if (entry == NULL) {
		*found = 0;
		return 0;
	}
	*found = 1;
	return ntohs(entry->s_port);
}

static char *php2go_service_by_port(int64_t port, const char *protocol) {
	struct servent *entry = getservbyport(
		htons((unsigned short) port), protocol);
	return entry == NULL ? NULL : php2go_copy_string(entry->s_name);
}

typedef struct {
	char name[IFNAMSIZ];
	uint64_t flags;
	int family;
	int has_family;
	int up;
	char address[INET6_ADDRSTRLEN];
	char netmask[INET6_ADDRSTRLEN];
	char broadcast[INET6_ADDRSTRLEN];
	char point_to_point[INET6_ADDRSTRLEN];
} php2go_interface_address;

static int php2go_sockaddr_string(
	const struct sockaddr *address, char *out, size_t length
) {
	if (address == NULL) {
		return 0;
	}
	switch (address->sa_family) {
	case AF_INET:
		return inet_ntop(AF_INET,
			&((const struct sockaddr_in *) address)->sin_addr,
			out, length) != NULL;
#ifdef AF_INET6
	case AF_INET6:
		return inet_ntop(AF_INET6,
			&((const struct sockaddr_in6 *) address)->sin6_addr,
			out, length) != NULL;
#endif
	default:
		return 0;
	}
}

static php2go_interface_address *php2go_interfaces(
	size_t *count, int *error_number
) {
	struct ifaddrs *addresses = NULL;
	*count = 0;
	*error_number = 0;
	if (getifaddrs(&addresses) != 0) {
		*error_number = errno;
		return NULL;
	}
	size_t total = 0;
	for (struct ifaddrs *address = addresses; address != NULL;
	     address = address->ifa_next) {
		total++;
	}
	php2go_interface_address *result = calloc(
		total == 0 ? 1 : total, sizeof(php2go_interface_address));
	if (result == NULL) {
		*error_number = ENOMEM;
		freeifaddrs(addresses);
		return NULL;
	}
	size_t index = 0;
	for (struct ifaddrs *address = addresses; address != NULL;
	     address = address->ifa_next, index++) {
		php2go_interface_address *entry = &result[index];
		strncpy(entry->name, address->ifa_name, IFNAMSIZ - 1);
		entry->flags = address->ifa_flags;
		entry->up = (address->ifa_flags & IFF_UP) != 0;
		if (address->ifa_addr != NULL) {
			entry->has_family = 1;
			entry->family = address->ifa_addr->sa_family;
			php2go_sockaddr_string(address->ifa_addr,
				entry->address, sizeof(entry->address));
		}
		php2go_sockaddr_string(address->ifa_netmask,
			entry->netmask, sizeof(entry->netmask));
		if ((address->ifa_flags & IFF_BROADCAST) != 0) {
			php2go_sockaddr_string(address->ifa_broadaddr,
				entry->broadcast, sizeof(entry->broadcast));
		}
		if ((address->ifa_flags & IFF_POINTOPOINT) != 0) {
			php2go_sockaddr_string(address->ifa_dstaddr,
				entry->point_to_point, sizeof(entry->point_to_point));
		}
	}
	freeifaddrs(addresses);
	*count = total;
	return result;
}
static struct timeval php2go_unique_id_previous = { 0, 0 };

static void php2go_unique_id_time(uint32_t *seconds, uint32_t *microseconds) {
	struct timeval current;
	do {
		(void) gettimeofday(&current, NULL);
	} while (current.tv_sec == php2go_unique_id_previous.tv_sec &&
	         current.tv_usec == php2go_unique_id_previous.tv_usec);
	php2go_unique_id_previous = current;
	*seconds = (uint32_t) ((int) current.tv_sec);
	*microseconds = (uint32_t) (current.tv_usec % 0x100000);
}

static char *php2go_read_link(const char *path, int *error_number) {
	char buffer[4096];
	ssize_t length = readlink(path, buffer, sizeof(buffer) - 1);
	if (length < 0) {
		*error_number = errno;
		return NULL;
	}
	char *result = malloc((size_t) length + 1);
	if (result == NULL) {
		*error_number = ENOMEM;
		return NULL;
	}
	memcpy(result, buffer, (size_t) length);
	result[length] = 0;
	*error_number = 0;
	return result;
}

*/
import "C"

import (
	"sync"
	"unsafe"
)

// The legacy lookup functions return pointers into process-global storage.
// Serialize each call and copy its result before another lookup can replace it.
var databaseMu sync.Mutex

func Available() bool { return true }

var uniqueIDMu sync.Mutex

func UniqueIDTime() (uint32, uint32, bool) {
	uniqueIDMu.Lock()
	defer uniqueIDMu.Unlock()
	var seconds C.uint32_t
	var microseconds C.uint32_t
	C.php2go_unique_id_time(&seconds, &microseconds)
	return uint32(seconds), uint32(microseconds), true
}

func ReadLink(path string) (string, int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var errorNumber C.int
	result := C.php2go_read_link(cpath, &errorNumber)
	if result == nil {
		return "", int(errorNumber), true
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), 0, true
}

type InterfaceAddress struct {
	Name         string
	Flags        uint64
	Family       int
	HasFamily    bool
	Up           bool
	Address      string
	Netmask      string
	Broadcast    string
	PointToPoint string
}

func Interfaces() ([]InterfaceAddress, int) {
	var count C.size_t
	var errorNumber C.int
	result := C.php2go_interfaces(&count, &errorNumber)
	if result == nil {
		return nil, int(errorNumber)
	}
	defer C.free(unsafe.Pointer(result))
	raw := unsafe.Slice(
		(*C.php2go_interface_address)(unsafe.Pointer(result)), int(count))
	interfaces := make([]InterfaceAddress, len(raw))
	for index := range raw {
		entry := &raw[index]
		interfaces[index] = InterfaceAddress{
			Name:         C.GoString(&entry.name[0]),
			Flags:        uint64(entry.flags),
			Family:       int(entry.family),
			HasFamily:    entry.has_family != 0,
			Up:           entry.up != 0,
			Address:      C.GoString(&entry.address[0]),
			Netmask:      C.GoString(&entry.netmask[0]),
			Broadcast:    C.GoString(&entry.broadcast[0]),
			PointToPoint: C.GoString(&entry.point_to_point[0]),
		}
	}
	return interfaces, 0
}

func HostByName(name string) (string, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	result := C.php2go_host_by_name(cname)
	if result == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), true
}

func HostByNameList(name string) ([]string, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var count C.size_t
	result := C.php2go_host_by_name_list(cname, &count)
	if result == nil {
		return nil, false
	}
	defer C.free(unsafe.Pointer(result))
	addresses := make([]string, int(count))
	for index := range addresses {
		addresses[index] = C.GoString(C.php2go_host_list_at(
			result, C.size_t(index)))
	}
	return addresses, true
}

func HostByAddress(ip string) (string, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cip := C.CString(ip)
	defer C.free(unsafe.Pointer(cip))
	result := C.php2go_host_by_address(cip)
	if result == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), true
}

func ProtocolByName(name string) (int64, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var found C.int
	result := C.php2go_protocol_by_name(cname, &found)
	return int64(result), found != 0
}

func ProtocolByNumber(number int64) (string, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	result := C.php2go_protocol_by_number(C.int64_t(number))
	if result == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), true
}

func ServiceByName(name, protocol string) (int64, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	cprotocol := C.CString(protocol)
	defer C.free(unsafe.Pointer(cprotocol))
	var found C.int
	result := C.php2go_service_by_name(cname, cprotocol, &found)
	return int64(result), found != 0
}

func ServiceByPort(port int64, protocol string) (string, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cprotocol := C.CString(protocol)
	defer C.free(unsafe.Pointer(cprotocol))
	result := C.php2go_service_by_port(C.int64_t(port), cprotocol)
	if result == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), true
}
