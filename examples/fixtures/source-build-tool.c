#include <stdio.h>

#ifdef __GLIBC__
#error "this example is intended for Alpine musl"
#endif

int main(void) {
    puts("built by AlpineForm on musl");
    return 0;
}
