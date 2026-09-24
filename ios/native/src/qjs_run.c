/*
 * The part of the qjs command line yt-dlp uses, in-process: `qjs --help` for
 * its version, and `qjs --script FILE` to run a challenge solver that answers
 * through console.log. Each run gets its own runtime on its own thread, with a
 * stack deep enough for the solver, so runs are independent and concurrent.
 * A cancel interrupts a run in progress.
 */
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "quickjs.h"

#include "reverb_native.h"

/* The solver recurses deeply; iOS gives secondary threads 512 KiB. */
#define QJS_THREAD_STACK (64u << 20)
#define QJS_MAX_STACK (60u << 20)

struct qjs_run {
    const char *script;
    int64_t token;
    FILE *out;
    FILE *err;
    int status;
    volatile int cancelled;
    struct qjs_run *next;
};

/* The runs in progress, for a cancel to find by token. */
static pthread_mutex_t runs_mu = PTHREAD_MUTEX_INITIALIZER;
static struct qjs_run *runs;

static int interrupted(JSRuntime *rt, void *opaque)
{
    struct qjs_run *r = opaque;
    return r->cancelled;
}

static void write_args(JSContext *ctx, FILE *f, int argc, JSValueConst *argv)
{
    for (int i = 0; i < argc; i++) {
        size_t len;
        const char *s = JS_ToCStringLen(ctx, &len, argv[i]);
        if (i)
            fputc(' ', f);
        if (s) {
            fwrite(s, 1, len, f);
            JS_FreeCString(ctx, s);
        }
    }
    fputc('\n', f);
}

static struct qjs_run *run_of(JSContext *ctx)
{
    return JS_GetContextOpaque(ctx);
}

static JSValue js_log(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
    write_args(ctx, run_of(ctx)->out, argc, argv);
    return JS_UNDEFINED;
}

static JSValue js_warn(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
    write_args(ctx, run_of(ctx)->err, argc, argv);
    return JS_UNDEFINED;
}

static void report_exception(JSContext *ctx, FILE *err)
{
    JSValue exc = JS_GetException(ctx);
    const char *msg = JS_ToCString(ctx, exc);
    fprintf(err, "%s\n", msg ? msg : "exception");
    if (msg)
        JS_FreeCString(ctx, msg);
    if (JS_IsError(exc)) {
        JSValue stack = JS_GetPropertyStr(ctx, exc, "stack");
        const char *s = JS_ToCString(ctx, stack);
        if (s) {
            fputs(s, err);
            JS_FreeCString(ctx, s);
        }
        JS_FreeValue(ctx, stack);
    }
    JS_FreeValue(ctx, exc);
}

static char *read_file(const char *path, size_t *len)
{
    FILE *f = fopen(path, "rb");
    if (!f)
        return NULL;
    char *buf = NULL;
    size_t cap = 0, n = 0;
    for (;;) {
        if (n + 65536 + 1 > cap) {
            cap = (n + 65536 + 1) * 2;
            char *b = realloc(buf, cap);
            if (!b) {
                free(buf);
                fclose(f);
                return NULL;
            }
            buf = b;
        }
        size_t got = fread(buf + n, 1, 65536, f);
        n += got;
        if (got < 65536)
            break;
    }
    fclose(f);
    buf[n] = 0;
    *len = n;
    return buf;
}

static void *run_script(void *arg)
{
    struct qjs_run *r = arg;
    size_t len;
    char *src = read_file(r->script, &len);
    if (!src) {
        fprintf(r->err, "qjs: could not read %s\n", r->script);
        r->status = 1;
        return NULL;
    }
    JSRuntime *rt = JS_NewRuntime();
    JSContext *ctx = rt ? JS_NewContext(rt) : NULL;
    if (!ctx) {
        fprintf(r->err, "qjs: out of memory\n");
        r->status = 1;
        if (rt)
            JS_FreeRuntime(rt);
        free(src);
        return NULL;
    }
    JS_SetMaxStackSize(rt, QJS_MAX_STACK);
    JS_SetInterruptHandler(rt, interrupted, r);
    JS_SetContextOpaque(ctx, r);

    JSValue global = JS_GetGlobalObject(ctx);
    JSValue console = JS_NewObject(ctx);
    JS_SetPropertyStr(ctx, console, "log", JS_NewCFunction(ctx, js_log, "log", 1));
    JS_SetPropertyStr(ctx, console, "info", JS_NewCFunction(ctx, js_log, "info", 1));
    JS_SetPropertyStr(ctx, console, "debug", JS_NewCFunction(ctx, js_log, "debug", 1));
    JS_SetPropertyStr(ctx, console, "warn", JS_NewCFunction(ctx, js_warn, "warn", 1));
    JS_SetPropertyStr(ctx, console, "error", JS_NewCFunction(ctx, js_warn, "error", 1));
    JS_SetPropertyStr(ctx, global, "console", console);
    JS_SetPropertyStr(ctx, global, "print", JS_NewCFunction(ctx, js_log, "print", 1));
    JS_FreeValue(ctx, global);

    r->status = 0;
    JSValue v = JS_Eval(ctx, src, len, r->script, JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(v)) {
        report_exception(ctx, r->err);
        r->status = 1;
    }
    JS_FreeValue(ctx, v);
    /* Settle any promises the script left. */
    while (r->status == 0) {
        JSContext *jctx;
        int ret = JS_ExecutePendingJob(rt, &jctx);
        if (ret == 0)
            break;
        if (ret < 0) {
            report_exception(jctx, r->err);
            r->status = 1;
        }
    }
    JS_FreeContext(ctx);
    JS_FreeRuntime(rt);
    free(src);
    return NULL;
}

int reverb_qjs_run(int argc, char **argv, int64_t token, struct reverb_output *out)
{
    const char *script = NULL;
    int help = 0;
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--script") == 0 && i + 1 < argc)
            script = argv[++i];
        else if (strcmp(argv[i], "--help") == 0 || strcmp(argv[i], "-h") == 0)
            help = 1;
        else if (argv[i][0] != '-' && !script)
            script = argv[i];
    }
    if (reverb_output_open(out) < 0)
        return -1;
    if (help || !script) {
        /* yt-dlp reads the version from the help text's first line. */
        fprintf(out->out, "QuickJS-ng version %s\n", JS_GetVersion());
        reverb_output_close(out);
        return help ? 0 : 1;
    }

    struct qjs_run r = {.script = script, .token = token, .out = out->out, .err = out->err, .status = 1};
    pthread_mutex_lock(&runs_mu);
    r.next = runs;
    runs = &r;
    pthread_mutex_unlock(&runs_mu);

    pthread_attr_t attr;
    pthread_t th;
    pthread_attr_init(&attr);
    pthread_attr_setstacksize(&attr, QJS_THREAD_STACK);
    if (pthread_create(&th, &attr, run_script, &r) == 0)
        pthread_join(th, NULL);
    else
        fprintf(out->err, "qjs: could not start a thread\n");
    pthread_attr_destroy(&attr);

    pthread_mutex_lock(&runs_mu);
    for (struct qjs_run **p = &runs; *p; p = &(*p)->next) {
        if (*p == &r) {
            *p = r.next;
            break;
        }
    }
    pthread_mutex_unlock(&runs_mu);

    reverb_output_close(out);
    return r.status;
}

void reverb_qjs_cancel(int64_t token)
{
    pthread_mutex_lock(&runs_mu);
    for (struct qjs_run *r = runs; r; r = r->next)
        if (r->token == token)
            r->cancelled = 1;
    pthread_mutex_unlock(&runs_mu);
}

const char *reverb_qjs_version(void)
{
    return JS_GetVersion();
}
