/*
 * Runs ffmpeg's and ffprobe's programs in-process, one at a time, as if each
 * run were a fresh process. See fftools_prefix.h for how their sources are
 * compiled; this file is compiled normally.
 */
#include <dlfcn.h>
#include <mach-o/getsect.h>
#include <mach-o/loader.h>
#include <pthread.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "libavutil/avutil.h"
#include "libavutil/log.h"

#include "reverb_native.h"

int reverb_ffmpeg_main(int argc, char **argv);
int reverb_ffprobe_main(int argc, char **argv);

FILE *reverb_fft_stdout;
FILE *reverb_fft_stderr;

/* The termination handler ffmpeg installed for this run, if any. */
static void (*term_handler)(int);
static int cancel_requested;
static pthread_mutex_t cancel_mu = PTHREAD_MUTEX_INITIALIZER;

void (*reverb_fft_signal(int sig, void (*handler)(int)))(int)
{
    if (sig != SIGTERM || handler == SIG_IGN || handler == SIG_DFL)
        return SIG_DFL;
    pthread_mutex_lock(&cancel_mu);
    term_handler = handler;
    /* A cancel that arrived while ffmpeg was still parsing its options. */
    if (cancel_requested == 1) {
        cancel_requested = 2;
        handler(SIGTERM);
    }
    pthread_mutex_unlock(&cancel_mu);
    return SIG_DFL;
}

/* The fftools sections: their pristine contents and where they live. */
struct fft_section {
    const char *name;
    uint8_t *addr;
    unsigned long size;
    uint8_t *pristine;
};

static struct fft_section sections[] = {
    {"__fft_data", NULL, 0, NULL},
    {"__fft_bss", NULL, 0, NULL},
};

static pthread_mutex_t run_mu = PTHREAD_MUTEX_INITIALIZER;
static int snapshotted;
static int64_t running_token;

/* Tokens cancelled before their run started. */
#define PENDING_CANCELS 16
static int64_t pending_cancels[PENDING_CANCELS];
static int pending_next;

static int take_pending_cancel(int64_t token)
{
    for (int i = 0; i < PENDING_CANCELS; i++) {
        if (pending_cancels[i] == token) {
            pending_cancels[i] = 0;
            return 1;
        }
    }
    return 0;
}

static int snapshot(void)
{
    Dl_info info;
    if (!dladdr((const void *)reverb_ffmpeg_main, &info) || !info.dli_fbase)
        return -1;
    const struct mach_header_64 *mh = info.dli_fbase;
    for (size_t i = 0; i < sizeof(sections) / sizeof(sections[0]); i++) {
        struct fft_section *s = &sections[i];
        s->addr = getsectiondata(mh, "__DATA", s->name, &s->size);
        if (!s->addr || !s->size)
            continue;
        s->pristine = malloc(s->size);
        if (!s->pristine)
            return -1;
        memcpy(s->pristine, s->addr, s->size);
    }
    return 0;
}

static void restore(void)
{
    for (size_t i = 0; i < sizeof(sections) / sizeof(sections[0]); i++) {
        struct fft_section *s = &sections[i];
        if (s->pristine)
            memcpy(s->addr, s->pristine, s->size);
    }
}

static int print_prefix = 1;

static void log_to_run(void *avcl, int level, const char *fmt, va_list vl)
{
    char line[2048];
    if (level > av_log_get_level())
        return;
    av_log_format_line2(avcl, level, fmt, vl, line, sizeof(line), &print_prefix);
    fputs(line, reverb_fft_stderr);
}

int reverb_fftool_run(const char *tool, int argc, char **argv, int64_t token,
                      struct reverb_output *out)
{
    int (*entry)(int, char **);
    if (strcmp(tool, "ffmpeg") == 0)
        entry = reverb_ffmpeg_main;
    else if (strcmp(tool, "ffprobe") == 0)
        entry = reverb_ffprobe_main;
    else
        return -1;

    pthread_mutex_lock(&run_mu);
    if (!snapshotted) {
        if (snapshot() < 0) {
            pthread_mutex_unlock(&run_mu);
            return -1;
        }
        snapshotted = 1;
    }
    restore();

    if (reverb_output_open(out) < 0) {
        pthread_mutex_unlock(&run_mu);
        return -1;
    }
    reverb_fft_stdout = out->out;
    reverb_fft_stderr = out->err;
    print_prefix = 1;
    av_log_set_level(AV_LOG_INFO);
    av_log_set_flags(0);
    av_log_set_callback(log_to_run);

    pthread_mutex_lock(&cancel_mu);
    int cancelled = take_pending_cancel(token);
    running_token = token;
    cancel_requested = 0;
    term_handler = NULL;
    pthread_mutex_unlock(&cancel_mu);

    int rc = cancelled ? 255 : entry(argc, argv);

    pthread_mutex_lock(&cancel_mu);
    running_token = 0;
    term_handler = NULL;
    pthread_mutex_unlock(&cancel_mu);

    av_log_set_callback(av_log_default_callback);
    reverb_fft_stdout = NULL;
    reverb_fft_stderr = NULL;
    reverb_output_close(out);
    pthread_mutex_unlock(&run_mu);
    return rc;
}

void reverb_fftool_cancel(int64_t token)
{
    pthread_mutex_lock(&cancel_mu);
    if (running_token == token) {
        /* Once: ffmpeg exits the process after its fourth signal. */
        if (!cancel_requested) {
            cancel_requested = 1;
            if (term_handler) {
                cancel_requested = 2;
                term_handler(SIGTERM);
            }
        }
    } else {
        pending_cancels[pending_next] = token;
        pending_next = (pending_next + 1) % PENDING_CANCELS;
    }
    pthread_mutex_unlock(&cancel_mu);
}

const char *reverb_ffmpeg_version(void)
{
    return av_version_info();
}
