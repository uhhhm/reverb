//go:build pyembed

#ifndef REVERB_EMBEDDED_H
#define REVERB_EMBEDDED_H

// Starts the interpreter and the launcher. paths holds npaths directories to
// append to sys.path. On failure it returns -1 and sets *err, which the
// caller frees.
int reverb_py_start(const char *home, const char **paths, int npaths, const char *launcher,
                    const char *tools_dir, const char *spotdl_home, int write_bytecode,
                    char **err);

// Runs `python -m module args` with its output on fd. Returns the exit
// status, 130 when cancelled, or -1 with *err set when it could not run.
int reverb_py_run(const char *module, const char **args, int nargs, int fd, long long token,
                  char **err);

// Cancels the run with token, if it is running.
void reverb_py_cancel(long long token);

#endif
