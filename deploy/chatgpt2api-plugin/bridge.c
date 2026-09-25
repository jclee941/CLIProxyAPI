#include "bridge.h"

int valid_host_api(const cliproxy_host_api *host) {
    return host != NULL && host->abi_version == 1 &&
           host->call != NULL && host->free_buffer != NULL;
}
