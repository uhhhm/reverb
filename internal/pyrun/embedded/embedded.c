//go:build pyembed

#define PY_SSIZE_T_CLEAN
#include <Python.h>

#include <stdlib.h>
#include <string.h>

#include "embedded.h"

// The native tools' module, linked from libreverbnative.
extern PyObject *PyInit__reverb_native(void);

static PyObject *launcher;
static PyObject *cancelled;

static char *error_text(const char *what)
{
    PyObject *type, *value, *tb;
    PyErr_Fetch(&type, &value, &tb);
    PyErr_NormalizeException(&type, &value, &tb);
    const char *msg = NULL;
    PyObject *str = value ? PyObject_Str(value) : NULL;
    if (str)
        msg = PyUnicode_AsUTF8(str);
    size_t n = strlen(what) + (msg ? strlen(msg) : 0) + 3;
    char *out = malloc(n);
    if (out)
        snprintf(out, n, "%s: %s", what, msg ? msg : "unknown error");
    Py_XDECREF(str);
    Py_XDECREF(type);
    Py_XDECREF(value);
    Py_XDECREF(tb);
    PyErr_Clear();
    return out;
}

static char *status_text(const char *what, PyStatus status)
{
    const char *msg = status.err_msg ? status.err_msg : "failed";
    size_t n = strlen(what) + strlen(msg) + 3;
    char *out = malloc(n);
    if (out)
        snprintf(out, n, "%s: %s", what, msg);
    return out;
}

int reverb_py_start(const char *home, const char **paths, int npaths, const char *source,
                    const char *tools_dir, const char *spotdl_home, int write_bytecode,
                    char **err)
{
    if (PyImport_AppendInittab("_reverb_native", PyInit__reverb_native) < 0) {
        *err = strdup("register _reverb_native");
        return -1;
    }
    PyConfig config;
    PyConfig_InitIsolatedConfig(&config);
    // The app owns the process's signals.
    config.install_signal_handlers = 0;
    // Threads a run starts write to that run's output (see launcher.py).
    config.thread_inherit_context = 1;
    config.buffered_stdio = 0;
    config.write_bytecode = write_bytecode;
    PyStatus status;
    if (home[0]) {
        status = PyConfig_SetBytesString(&config, &config.home, home);
        if (PyStatus_Exception(status))
            goto fail;
    }
    status = Py_InitializeFromConfig(&config);
    if (PyStatus_Exception(status))
        goto fail;
    PyConfig_Clear(&config);

    PyObject *path = PySys_GetObject("path");
    for (int i = 0; path && i < npaths; i++) {
        PyObject *p = PyUnicode_DecodeFSDefault(paths[i]);
        if (!p || PyList_Append(path, p) < 0) {
            Py_XDECREF(p);
            *err = error_text("sys.path");
            return -1;
        }
        Py_DECREF(p);
    }

    PyObject *code = Py_CompileString(source, "reverb_launcher.py", Py_file_input);
    PyObject *mod = code ? PyImport_ExecCodeModule("reverb_launcher", code) : NULL;
    Py_XDECREF(code);
    if (!mod) {
        *err = error_text("load launcher");
        return -1;
    }
    PyObject *res = PyObject_CallMethod(mod, "setup", "ss", tools_dir, spotdl_home);
    if (!res) {
        *err = error_text("launcher setup");
        Py_DECREF(mod);
        return -1;
    }
    Py_DECREF(res);
    cancelled = PyObject_GetAttrString(mod, "Cancelled");
    launcher = mod;
    // Runs take the GIL from whichever thread they arrive on.
    PyEval_SaveThread();
    return 0;

fail:
    *err = status_text("start Python", status);
    PyConfig_Clear(&config);
    return -1;
}

int reverb_py_run(const char *module, const char **args, int nargs, int fd, long long token,
                  char **err)
{
    PyGILState_STATE g = PyGILState_Ensure();
    int rc = -1;
    PyObject *list = PyList_New(nargs);
    for (int i = 0; list && i < nargs; i++) {
        PyObject *a = PyUnicode_DecodeFSDefault(args[i]);
        if (!a) {
            Py_CLEAR(list);
            break;
        }
        PyList_SET_ITEM(list, i, a);
    }
    PyObject *res = list ? PyObject_CallMethod(launcher, "run", "sOiL", module, list, fd, token) : NULL;
    Py_XDECREF(list);
    if (res) {
        rc = (int)PyLong_AsLong(res);
        Py_DECREF(res);
    } else if (PyErr_ExceptionMatches(cancelled) || PyErr_ExceptionMatches(PyExc_KeyboardInterrupt)) {
        // The cancel landed outside the module, as the run was ending.
        PyErr_Clear();
        rc = 130;
    } else {
        *err = error_text(module);
    }
    PyGILState_Release(g);
    return rc;
}

int reverb_py_has_module(const char *module)
{
    PyGILState_STATE g = PyGILState_Ensure();
    PyObject *res = PyObject_CallMethod(launcher, "has_module", "s", module);
    int has = res && PyObject_IsTrue(res) == 1;
    Py_XDECREF(res);
    PyErr_Clear();
    PyGILState_Release(g);
    return has;
}

void reverb_py_cancel(long long token)
{
    PyGILState_STATE g = PyGILState_Ensure();
    PyObject *res = PyObject_CallMethod(launcher, "cancel", "L", token);
    if (res) {
        unsigned long thread = PyLong_AsUnsignedLong(res);
        // Still holding the GIL since cancel checked the run is registered.
        if (thread)
            PyThreadState_SetAsyncExc(thread, cancelled);
        Py_DECREF(res);
    }
    PyErr_Clear();
    PyGILState_Release(g);
}
