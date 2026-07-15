#include <stdio.h>

#ifdef __GLIBC__
#error "source-build fixture must compile against musl"
#endif

#ifndef BUILD_VARIANT
#define BUILD_VARIANT "source-v1"
#endif

int main(void) {
    puts("alpineform-musl-" BUILD_VARIANT);
    return 0;
}
