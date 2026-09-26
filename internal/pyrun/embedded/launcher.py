"""Runs Python tools' command lines inside an embedded interpreter.

A phone cannot spawn processes, so yt-dlp and spotDL run as modules in the
app's own interpreter, several at once, each on the calling thread. This module
gives each run what a process would have had:

- its own argv and its own stdout and stderr, which go to the run's output
  file descriptor. Threads a run starts inherit its context, and so its
  output (the interpreter is configured with thread_inherit_context);
- ffmpeg, ffprobe and qjs, which the tools start as subprocesses, run
  in-process through _reverb_native instead;
- a cancel, which stops the run's native tools and raises Cancelled in its
  thread.

Everything here is set up once, by setup(), before either tool is imported.
"""

import contextvars
import importlib
import io
import itertools
import os
import runpy
import shutil
import signal
import subprocess
import sys
import threading
import traceback

import _reverb_native

TOOLS = ("ffmpeg", "ffprobe", "qjs")


class Cancelled(BaseException):
    """Raised in a run's thread when the run is cancelled."""


class _Run:
    def __init__(self, fd, argv, token):
        # A thread the run started can outlive it, and must not then write to
        # whatever file later reuses the number: writes go to a duplicate that
        # close() retires under the same lock.
        self.fd = os.dup(fd)
        self.closed = False
        self.argv = argv
        self.token = token
        self.thread = threading.get_ident()
        self.cancelled = False
        self.tools = set()
        self.lock = threading.Lock()

    def write(self, data):
        if isinstance(data, str):
            data = data.encode("utf-8", "replace")
        with self.lock:
            if self.closed:
                return
            while data:
                try:
                    n = os.write(self.fd, data)
                except OSError:
                    # The reader is gone; the run's output has nowhere to go.
                    return
                data = data[n:]

    def close(self):
        with self.lock:
            self.closed = True
            os.close(self.fd)


_current = contextvars.ContextVar("reverb_run", default=None)
_runs = {}
_runs_lock = threading.Lock()
# Readers are Python runs, not Go callers: a cancelled caller can return while
# Python is still unwinding. Activation never changes imports under such a run.
_package_gate = threading.Condition()
_package_readers = 0
_package_writer = False
_update_path = None
_tokens = itertools.count(1)
_config = {}


class _Stream(io.TextIOBase):
    """sys.stdout or sys.stderr: writes go to the current run's output."""

    def __init__(self, name, fallback):
        self._name = name
        self._fallback = fallback
        self.buffer = _BinaryStream(self)

    @property
    def encoding(self):
        return "utf-8"

    @property
    def errors(self):
        return "replace"

    @property
    def name(self):
        return "<%s>" % self._name

    def writable(self):
        return True

    def isatty(self):
        return False

    def fileno(self):
        raise io.UnsupportedOperation("an embedded run has no file descriptor")

    def write(self, s):
        run = _current.get()
        if run is not None:
            run.write(s)
        elif self._fallback is not None:
            try:
                self._fallback.write(s)
            except Exception:
                pass
        return len(s)

    def flush(self):
        pass


class _BinaryStream(io.RawIOBase):
    def __init__(self, text):
        self._text = text

    def writable(self):
        return True

    def write(self, b):
        run = _current.get()
        if run is not None:
            run.write(bytes(b))
        return len(b)


class _Argv(list):
    """sys.argv: the current run's."""

    def _get(self):
        run = _current.get()
        return run.argv if run is not None else ["python"]

    def __getitem__(self, i):
        return self._get()[i]

    def __len__(self):
        return len(self._get())

    def __iter__(self):
        return iter(self._get())

    def __contains__(self, x):
        return x in self._get()

    def __repr__(self):
        return repr(self._get())

    def __eq__(self, other):
        return self._get() == other

    def copy(self):
        return list(self._get())


_OriginalPopen = subprocess.Popen


def _tool_of(args):
    if isinstance(args, (str, bytes, os.PathLike)):
        return None
    try:
        name = os.path.basename(os.fsdecode(args[0]))
    except (IndexError, TypeError):
        return None
    for tool in TOOLS:
        if name == tool or name == tool + ".exe":
            return tool
    return None


def _tool_argv(tool, args):
    argv = [tool] + [os.fsdecode(a) for a in args[1:]]
    if tool != "ffmpeg":
        return argv
    out = [argv[0], "-nostdin"]
    rest = argv[1:]
    i = 0
    while i < len(rest):
        # Progress to stdout would reach the app's own file descriptor 1.
        if rest[i] == "-progress" and i + 1 < len(rest) and rest[i + 1] in ("-", "pipe:1", "pipe:"):
            i += 2
            continue
        if rest[i] != "-nostdin":
            out.append(rest[i])
        i += 1
    return out


class Popen(_OriginalPopen):
    """subprocess.Popen that runs ffmpeg, ffprobe and qjs in-process.

    Anything else is left to the real Popen, which cannot start a process on
    a phone and says so.
    """

    _tool = None

    def __init__(self, args, bufsize=-1, executable=None, stdin=None, stdout=None, stderr=None,
                 *rest, text=None, encoding=None, errors=None, universal_newlines=None, **kwargs):
        tool = _tool_of(args)
        if tool is None:
            super().__init__(args, bufsize, executable, stdin, stdout, stderr, *rest,
                             text=text, encoding=encoding, errors=errors,
                             universal_newlines=universal_newlines, **kwargs)
            return
        self._tool = tool
        self._run = _current.get()
        self._token = next(_tokens)
        self.args = args
        self.pid = -1
        self.returncode = None
        self.text_mode = bool(text or encoding or errors or universal_newlines)
        self.encoding = encoding
        self.errors = errors
        self._done = threading.Event()

        self.stdin = open(os.devnull, "w" if self.text_mode else "wb") if stdin == subprocess.PIPE else None
        self.stdout, out_w = self._pipe(stdout)
        if stderr == subprocess.STDOUT:
            self.stderr, err_w = None, "stdout"
        else:
            self.stderr, err_w = self._pipe(stderr)
        self._sinks = (out_w, err_w)

        if self._run is not None:
            with self._run.lock:
                if self._run.cancelled:
                    raise Cancelled()
                self._run.tools.add(self)
        argv = _tool_argv(tool, args)
        threading.Thread(target=self._work, args=(argv,), name="reverb-" + tool, daemon=True).start()

    def _pipe(self, dest):
        """The reading end for the caller and where the tool's output goes: a
        file descriptor to write and close, "null", or None for the run's own
        output."""
        if dest is None:
            return None, None
        if dest == subprocess.DEVNULL:
            return None, "null"
        if dest != subprocess.PIPE:
            fd = dest if isinstance(dest, int) else dest.fileno()
            return None, os.dup(fd)
        r, w = os.pipe()
        if self.text_mode:
            return open(r, "r", encoding=self.encoding or "utf-8", errors=self.errors or "strict"), w
        return open(r, "rb"), w

    @staticmethod
    def _write(fd, data):
        try:
            view = memoryview(data)
            while view:
                n = os.write(fd, view)
                view = view[n:]
        except OSError:
            pass
        finally:
            os.close(fd)

    def _work(self, argv):
        try:
            status, out, err = _reverb_native.run_tool(self._tool, argv, self._token)
        except Exception as e:
            status, out, err = 127, b"", ("%s: %s\n" % (self._tool, e)).encode()
        out_w, err_w = self._sinks
        if err_w == "stdout":
            out, err, err_w = out + err, b"", None
        writers = []
        for data, fd, stream in ((out, out_w, sys.stdout), (err, err_w, sys.stderr)):
            if fd == "null":
                continue
            if fd is not None:
                # Each pipe drains on its own, so neither can block the other.
                t = threading.Thread(target=self._write, args=(fd, data), daemon=True)
                t.start()
                writers.append(t)
            elif data:
                stream.buffer.write(data)
        self.returncode = status
        if self._run is not None:
            with self._run.lock:
                self._run.tools.discard(self)
        self._done.set()
        for t in writers:
            t.join()

    def poll(self):
        if self._tool is None:
            return super().poll()
        return self.returncode if self._done.is_set() else None

    def wait(self, timeout=None):
        if self._tool is None:
            return super().wait(timeout)
        if not self._done.wait(timeout):
            raise subprocess.TimeoutExpired(self.args, timeout)
        return self.returncode

    def communicate(self, input=None, timeout=None):
        if self._tool is None:
            return super().communicate(input, timeout)
        if self.stdin:
            self.stdin.close()
        out = err = None
        if self.stdout and self.stderr:
            box = {}
            t = threading.Thread(target=lambda: box.update(err=self.stderr.read()), daemon=True)
            t.start()
            out = self.stdout.read()
            t.join()
            err = box.get("err")
        elif self.stdout:
            out = self.stdout.read()
        elif self.stderr:
            err = self.stderr.read()
        self.wait(timeout)
        return out, err

    def send_signal(self, sig):
        if self._tool is None:
            return super().send_signal(sig)
        self.kill()

    def terminate(self):
        if self._tool is None:
            return super().terminate()
        self.kill()

    def kill(self):
        if self._tool is None:
            return super().kill()
        if not self._done.is_set():
            _reverb_native.cancel_tool(self._tool, self._token)

    def __exit__(self, *exc):
        if self._tool is None:
            return super().__exit__(*exc)
        for f in (self.stdout, self.stderr, self.stdin):
            if f:
                f.close()
        self.wait()
        return False

    def __del__(self):
        if self._tool is None:
            super().__del__()


def _which(cmd, mode=os.F_OK | os.X_OK, path=None, _which=shutil.which):
    name = os.path.basename(os.fsdecode(cmd))
    if name in TOOLS:
        return os.path.join(_config["tools_dir"], name)
    return _which(cmd, mode, path)


def _signal(sig, handler, _signal=signal.signal):
    # The tools install handlers for their own process; this one is the app's.
    if threading.current_thread() is threading.main_thread() and _config.get("allow_signals"):
        return _signal(sig, handler)
    return signal.SIG_DFL


# Per-module adjustments for running a tool repeatedly inside one process.
_serialized = {"spotdl": threading.Lock()}


def _before_spotdl():
    # spotDL keeps its Spotify client on the class, and refuses a second one.
    try:
        from spotdl.utils import spotify
    except ImportError:
        return
    spotify.SpotifyClient._instance = None
    spotify.SpotifyClient._use_official_api = False


_before = {"spotdl": _before_spotdl}

# The tools are imported by their first run, not by setup, so starting the
# interpreter stays cheap.
_prepared = False
_prepare_lock = threading.Lock()


def _prepare():
    global _prepared
    with _prepare_lock:
        if _prepared:
            return
        _prefer_quickjs()
        _prepare_spotdl(_config.get("spotdl_home"))
        _prepared = True


def _prepare_spotdl(home):
    import importlib.util
    import types

    # The web UI's dependencies (FastAPI, pydantic) are not bundled; Reverb
    # never runs spotDL's web operation.
    if importlib.util.find_spec("fastapi") is None:
        web = types.ModuleType("spotdl.console.web")

        def unavailable(*args, **kwargs):
            raise RuntimeError("spotDL's web operation is not available here")

        web.web = unavailable
        sys.modules["spotdl.console.web"] = web
    if home:
        try:
            from spotdl.utils import config
        except ImportError:
            return
        path = os.path.join(home, "spotdl")

        def spotdl_path():
            os.makedirs(path, exist_ok=True)
            from pathlib import Path
            return Path(path)

        config.get_spotdl_path = spotdl_path


def _prefer_quickjs():
    # yt-dlp enables deno unless told otherwise; QuickJS is what a phone has.
    try:
        import yt_dlp
    except ImportError:
        return
    init = yt_dlp.YoutubeDL.__init__

    def __init__(self, params=None, *args, **kwargs):
        params = dict(params or {})
        runtimes = params.get("js_runtimes")
        # Unset, or the command line's default: deno, wherever it is.
        if runtimes is None or (set(runtimes) == {"deno"} and not (runtimes["deno"] or {}).get("path")):
            params["js_runtimes"] = {"quickjs": {}}
        init(self, params, *args, **kwargs)

    yt_dlp.YoutubeDL.__init__ = __init__


def setup(tools_dir, spotdl_home="", allow_signals=False):
    """Installs the routing. Idempotent."""
    if _config:
        return
    _config.update(tools_dir=tools_dir, spotdl_home=spotdl_home, allow_signals=allow_signals)
    os.makedirs(tools_dir, exist_ok=True)
    for tool in TOOLS:
        # Placeholders, so a check that the executable exists passes.
        p = os.path.join(tools_dir, tool)
        if not os.path.exists(p):
            with open(p, "w") as f:
                f.write("#!/bin/sh\nexit 127\n")
            os.chmod(p, 0o755)
    sys.stdout = _Stream("stdout", sys.__stdout__)
    sys.stderr = _Stream("stderr", sys.__stderr__)
    sys.argv = _Argv()
    subprocess.Popen = Popen
    shutil.which = _which
    signal.signal = _signal


def _exit_status(code):
    if code is None:
        return 0
    if isinstance(code, int):
        return code
    sys.stderr.write("%s\n" % code)
    return 1


def run(module, args, fd, token):
    """Runs `python -m module args`, writing its output to fd. Returns its
    exit status."""
    r = _Run(fd, [module] + list(args), token)
    with _runs_lock:
        _runs[token] = r
    lock = _serialized.get(module)
    reader = False
    locked = False
    try:
        if module != "__reverb_activate_ytdlp":
            global _package_readers
            with _package_gate:
                while _package_writer:
                    _package_gate.wait(0.1)
                _package_readers += 1
                reader = True
        if lock:
            lock.acquire()
            locked = True
        ctx = contextvars.copy_context()
        return ctx.run(_run_in_context, r, module)
    finally:
        if locked:
            lock.release()
        if reader:
            with _package_gate:
                _package_readers -= 1
                _package_gate.notify_all()
        with _runs_lock:
            _runs.pop(token, None)
        r.close()


def _run_in_context(r, module):
    _current.set(r)
    try:
        if r.cancelled:
            raise Cancelled()
        if module == "__reverb_activate_ytdlp":
            _activate_ytdlp(*r.argv[1:])
            return 0
        _prepare()
        before = _before.get(module)
        if before:
            before()
        runpy.run_module(module, run_name="__main__", alter_sys=False)
        return 0
    except SystemExit as e:
        return _exit_status(e.code)
    except Cancelled:
        return 130
    except KeyboardInterrupt:
        return 130 if r.cancelled else 1
    except BaseException:
        if r.cancelled:
            return 130
        r.write(traceback.format_exc())
        return 1


def _activate_ytdlp(directory, version):
    global _package_writer, _update_path, _prepared
    with _package_gate:
        while _package_writer:
            _package_gate.wait(0.1)
        _package_writer = True
    try:
        with _package_gate:
            while _package_readers:
                _package_gate.wait(0.1)
        old_path = list(sys.path)
        old_modules = {k: v for k, v in sys.modules.copy().items()
                       if k == "yt_dlp" or k.startswith("yt_dlp.")}
        try:
            for name in old_modules:
                sys.modules.pop(name, None)
            if _update_path in sys.path:
                sys.path.remove(_update_path)
            if directory:
                sys.path.insert(0, directory)
            importlib.invalidate_caches()
            import yt_dlp
            from yt_dlp.version import __version__
            from yt_dlp.extractor import gen_extractor_classes
            if version and __version__ != version:
                raise RuntimeError("yt-dlp package version does not match its manifest")
            # Build its extractor registry and downloader without network I/O.
            # Imports alone would miss incompatible runtime dependencies.
            if not gen_extractor_classes():
                raise RuntimeError("yt-dlp package has no extractors")
            _prefer_quickjs()
            with yt_dlp.YoutubeDL({"quiet": True, "no_warnings": True}) as ydl:
                if "quickjs" not in ydl.params.get("js_runtimes", {}):
                    raise RuntimeError("yt-dlp package cannot use QuickJS")
            _update_path = directory or None
            # Do not patch YoutubeDL twice during the first ordinary run.
            if not _prepared:
                _prepare_spotdl(_config.get("spotdl_home"))
                _prepared = True
        except BaseException:
            for name in list(sys.modules):
                if name == "yt_dlp" or name.startswith("yt_dlp."):
                    sys.modules.pop(name, None)
            sys.modules.update(old_modules)
            sys.path[:] = old_path
            importlib.invalidate_caches()
            raise
    finally:
        with _package_gate:
            _package_writer = False
            _package_gate.notify_all()


def has_module(name):
    """Whether a top-level module is installed, without importing it."""
    import importlib.util

    try:
        return importlib.util.find_spec(name) is not None
    except (ImportError, ValueError):
        return False


def cancel(token):
    """Stops a run's native tools and returns the thread to interrupt, or 0."""
    with _runs_lock:
        r = _runs.get(token)
    if r is None:
        return 0
    with r.lock:
        r.cancelled = True
        tools = list(r.tools)
    for p in tools:
        p.kill()
    # The caller interrupts the thread without releasing the GIL, so a run
    # still registered here cannot finish before the interrupt lands.
    with _runs_lock:
        if _runs.get(token) is not r:
            return 0
    return r.thread
