/*
 * The native tools yt-dlp and spotDL would spawn as processes (ffmpeg,
 * ffprobe, and QuickJS as yt-dlp's JavaScript runtime), run in-process instead
 * for a device that cannot spawn processes. The Python module _reverb_native
 * (reverb_native.c) is how the launcher reaches them.
 */
#ifndef REVERB_NATIVE_H
#define REVERB_NATIVE_H

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

/* A run's captured stdout and stderr. */
struct reverb_output {
    FILE *out;
    FILE *err;
    char *out_buf;
    size_t out_len;
    char *err_buf;
    size_t err_len;
};

/* Opens the capture streams; close flushes them into the buffers, which the
 * caller frees with reverb_output_free. */
int reverb_output_open(struct reverb_output *o);
void reverb_output_close(struct reverb_output *o);
void reverb_output_free(struct reverb_output *o);

/* Each returns the program's exit status, or -1 when it could not run.
 * token identifies the run to a cancel from another thread. */
int reverb_fftool_run(const char *tool, int argc, char **argv, int64_t token,
                      struct reverb_output *out);
void reverb_fftool_cancel(int64_t token);
const char *reverb_ffmpeg_version(void);

int reverb_qjs_run(int argc, char **argv, int64_t token, struct reverb_output *out);
void reverb_qjs_cancel(int64_t token);
const char *reverb_qjs_version(void);

#endif
