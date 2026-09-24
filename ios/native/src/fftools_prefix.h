/*
 * Force-included (-include) into every fftools source file, and nothing else,
 * so ffmpeg's and ffprobe's command-line programs can run inside the app,
 * which cannot spawn processes.
 *
 * - Their writable globals, static locals included, go to their own sections,
 *   which fftools_shim.c restores to their pristine contents before each run:
 *   the programs assume a fresh process and never reset their own state.
 * - stdout and stderr are the run's capture streams, not the process's.
 * - signal() records ffmpeg's termination handler instead of installing it, so
 *   a cancel can call it without taking over the app's signals.
 */
#ifndef REVERB_FFTOOLS_PREFIX_H
#define REVERB_FFTOOLS_PREFIX_H

#include <signal.h>
#include <stdio.h>

extern FILE *reverb_fft_stdout;
extern FILE *reverb_fft_stderr;
void (*reverb_fft_signal(int sig, void (*handler)(int)))(int);
int reverb_ffmpeg_main(int argc, char **argv);
int reverb_ffprobe_main(int argc, char **argv);

#undef stdout
#undef stderr
#define stdout reverb_fft_stdout
#define stderr reverb_fft_stderr
#define signal(sig, handler) reverb_fft_signal(sig, handler)
#define printf(...) fprintf(stdout, __VA_ARGS__)
#define vprintf(fmt, ap) vfprintf(stdout, fmt, ap)
#define puts(s) (fputs(s, stdout), fputc('\n', stdout))
#define putchar(c) fputc(c, stdout)

#pragma clang section bss = "__DATA,__fft_bss" data = "__DATA,__fft_data"

#endif
