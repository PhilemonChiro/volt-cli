#ifndef VOLT_HTTP_H
#define VOLT_HTTP_H

#include <stddef.h>
#include "volt/error.h"

// HTTP GET — fetch a URL, response body copied into buf.
// status_code receives the HTTP status (200, 404, etc). Can be NULL if you don't care.
// Returns VOLT_OK on successful request (even if status is 404 — check status_code).
// Returns VOLT_ERR_TIMEOUT or VOLT_ERR_HTTP_FAILED on connection failure.
volt_error_t http_get(const char *url, char *buf, size_t buf_size, int *status_code);

// HTTP POST — send body to URL, response copied into response buf.
// content_type defaults to "application/json" if NULL.
// status_code and response can be NULL if you don't need them.
volt_error_t http_post(const char *url, const char *body, const char *content_type,
                        char *response, size_t response_size, int *status_code);

#endif // VOLT_HTTP_H
