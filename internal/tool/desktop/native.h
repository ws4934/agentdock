#ifndef AGENTDOCK_DESKTOP_NATIVE_H
#define AGENTDOCK_DESKTOP_NATIVE_H
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
int ad_supported(void);
int ad_permissions(void);
int ad_request_permission(int permission);
char *ad_state(void);
char *ad_tree(int pid, int nodes, int depth);
int ad_capture(uint32_t display, int dimension, int timeout_ms, unsigned char **data, size_t *size, int *width, int *height);
int ad_press(int pid, const char *element);
int ad_activate(int pid);
int ad_mouse(int pid, int kind, double x, double y, int button, int count, uint64_t flags);
int ad_scroll(int pid, int dx, int dy);
int ad_key(int pid, uint16_t key, uint64_t flags);
int ad_text(int pid, const uint16_t *text, size_t length);
int ad_capture_window(uint32_t window, int pid, int dimension, int timeout_ms, unsigned char **data, size_t *size, int *width, int *height);
char *ad_window_tree(const char *window, int nodes, int depth);
int ad_window_input(const char *request);
int ad_background_pointer_supported(void);
#endif
