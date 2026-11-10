// Linux equivalent of transport_fence.c.
//
// The foreign API asks for an APS context even when generating only ADI data.
// On Linux the real APS connection is this project's Go TLS client
// (aps/client.go) using the account-private certificate in push.json. This
// shim is loaded with LD_PRELOAD into the ADI helper process only (never into
// the Mac APS bootstrap, never system-wide) and refuses that process's
// outgoing TCP connections to port 5223, so the native module cannot open a
// second push connection that would compete with the Go client.
//
// Build: gcc -shared -fPIC -O2 transport_fence_linux.c -o transport_fence.so -ldl
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <netinet/in.h>
#include <sys/socket.h>

typedef int (*connect_fn)(int, const struct sockaddr *, socklen_t);
static connect_fn original_connect;
static volatile int refused_count;

static int blocked(const struct sockaddr *address, socklen_t size) {
	if (!address) return 0;
	if (address->sa_family == AF_INET && size >= sizeof(struct sockaddr_in))
		return ntohs(((const struct sockaddr_in *)address)->sin_port) == 5223;
	if (address->sa_family == AF_INET6 && size >= sizeof(struct sockaddr_in6))
		return ntohs(((const struct sockaddr_in6 *)address)->sin6_port) == 5223;
	return 0;
}

__attribute__((visibility("default"))) int connect(int fd, const struct sockaddr *address, socklen_t size) {
	if (blocked(address, size)) {
		refused_count++;
		errno = ECONNREFUSED;
		return -1;
	}
	if (!original_connect) original_connect = (connect_fn)dlsym(RTLD_NEXT, "connect");
	if (!original_connect) { errno = ENOSYS; return -1; }
	return original_connect(fd, address, size);
}

// Returns 2 when the shim is the process-wide connect() (the same success
// value the Windows fence reports after patching its two imports).
__attribute__((visibility("default"))) int InstallTransportFence(void *module) {
	(void)module;
	connect_fn resolved = (connect_fn)dlsym(RTLD_DEFAULT, "connect");
	if (resolved != connect) return -1;
	if (!original_connect) original_connect = (connect_fn)dlsym(RTLD_NEXT, "connect");
	return original_connect ? 2 : -2;
}

__attribute__((visibility("default"))) int TransportFenceRefused(void) { return refused_count; }
