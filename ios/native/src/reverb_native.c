/*
 * _reverb_native: the Python module the launcher runs native tools through.
 *
 *   run_tool(name, argv, token) -> (status, stdout: bytes, stderr: bytes)
 *   cancel_tool(name, token)
 *   FFMPEG_VERSION, QUICKJS_VERSION
 *
 * name is "ffmpeg", "ffprobe" or "qjs"; argv includes the program name. The
 * GIL is released while a tool runs.
 */
#define PY_SSIZE_T_CLEAN
#include <Python.h>

#include <stdlib.h>
#include <string.h>

#include "reverb_native.h"

int reverb_output_open(struct reverb_output *o)
{
    o->out = open_memstream(&o->out_buf, &o->out_len);
    o->err = open_memstream(&o->err_buf, &o->err_len);
    if (!o->out || !o->err) {
        reverb_output_close(o);
        return -1;
    }
    return 0;
}

void reverb_output_close(struct reverb_output *o)
{
    if (o->out)
        fclose(o->out);
    if (o->err)
        fclose(o->err);
    o->out = NULL;
    o->err = NULL;
}

void reverb_output_free(struct reverb_output *o)
{
    reverb_output_close(o);
    free(o->out_buf);
    free(o->err_buf);
    memset(o, 0, sizeof(*o));
}

static void free_argv(char **argv, Py_ssize_t n)
{
    for (Py_ssize_t i = 0; i < n; i++)
        free(argv[i]);
    free(argv);
}

static PyObject *run_tool(PyObject *self, PyObject *args)
{
    const char *name;
    PyObject *seq;
    long long token;
    if (!PyArg_ParseTuple(args, "sOL", &name, &seq, &token))
        return NULL;
    PyObject *fast = PySequence_Fast(seq, "argv must be a sequence");
    if (!fast)
        return NULL;
    Py_ssize_t n = PySequence_Fast_GET_SIZE(fast);
    char **argv = calloc((size_t)n + 1, sizeof(char *));
    if (!argv) {
        Py_DECREF(fast);
        return PyErr_NoMemory();
    }
    for (Py_ssize_t i = 0; i < n; i++) {
        const char *s = PyUnicode_AsUTF8(PySequence_Fast_GET_ITEM(fast, i));
        if (!s || !(argv[i] = strdup(s))) {
            free_argv(argv, i);
            Py_DECREF(fast);
            return s ? PyErr_NoMemory() : NULL;
        }
    }
    Py_DECREF(fast);

    struct reverb_output out = {0};
    int status;
    char tool[16];
    snprintf(tool, sizeof(tool), "%s", name);
    Py_BEGIN_ALLOW_THREADS
    if (strcmp(tool, "qjs") == 0)
        status = reverb_qjs_run((int)n, argv, token, &out);
    else
        status = reverb_fftool_run(tool, (int)n, argv, token, &out);
    Py_END_ALLOW_THREADS
    free_argv(argv, n);

    if (status < 0 && !out.out_buf && !out.err_buf) {
        reverb_output_free(&out);
        PyErr_Format(PyExc_OSError, "could not run %s in-process", name);
        return NULL;
    }
    PyObject *res = Py_BuildValue("(iy#y#)", status,
                                  out.out_buf ? out.out_buf : "", (Py_ssize_t)out.out_len,
                                  out.err_buf ? out.err_buf : "", (Py_ssize_t)out.err_len);
    reverb_output_free(&out);
    return res;
}

static PyObject *cancel_tool(PyObject *self, PyObject *args)
{
    const char *name;
    long long token;
    if (!PyArg_ParseTuple(args, "sL", &name, &token))
        return NULL;
    if (strcmp(name, "qjs") == 0)
        reverb_qjs_cancel(token);
    else
        reverb_fftool_cancel(token);
    Py_RETURN_NONE;
}

static PyMethodDef methods[] = {
    {"run_tool", run_tool, METH_VARARGS, "Run a native tool in-process."},
    {"cancel_tool", cancel_tool, METH_VARARGS, "Cancel a native tool's run."},
    {NULL, NULL, 0, NULL},
};

static struct PyModuleDef module = {
    PyModuleDef_HEAD_INIT, "_reverb_native", NULL, -1, methods,
};

PyMODINIT_FUNC PyInit__reverb_native(void)
{
    PyObject *m = PyModule_Create(&module);
    if (!m)
        return NULL;
    if (PyModule_AddStringConstant(m, "FFMPEG_VERSION", reverb_ffmpeg_version()) < 0 ||
        PyModule_AddStringConstant(m, "QUICKJS_VERSION", reverb_qjs_version()) < 0) {
        Py_DECREF(m);
        return NULL;
    }
    return m;
}
