# Lists one Spotify playlist through spotDL's own Spotify client, which needs
# no Spotify app credentials. Run as `python -c <this> <playlist url>`.
#
# Playlist.get_metadata reads the playlist and its tracks in a few requests.
# The spotDL command line (`spotdl save`) would also fetch every track's full
# details, one request each, which takes minutes for a long playlist.
#
# Results are printed one JSON object per line behind a REVERB- marker, so
# spotDL's own log lines on the same output are never taken for data and no
# line grows with the playlist. REVERB-END says the listing is complete.
import json
import sys


def emit(kind, obj):
    # ASCII-only JSON survives any console encoding.
    print("REVERB-" + kind + " " + json.dumps(obj), flush=True)


def text(v):
    return v if isinstance(v, str) else ""


def main(url):
    try:
        from spotdl.types.playlist import Playlist
        from spotdl.utils.config import DEFAULT_CONFIG
        from spotdl.utils.spotify import SpotifyClient
    except ImportError as exc:
        emit("MISSING", {"error": str(exc)})
        return 3
    try:
        SpotifyClient.init(
            client_id=DEFAULT_CONFIG["client_id"],
            client_secret=DEFAULT_CONFIG["client_secret"],
            no_cache=True,
        )
        meta, songs = Playlist.get_metadata(url)
    except Exception as exc:
        emit("ERROR", {"error": str(exc) or type(exc).__name__})
        return 1
    emit("PLAYLIST", {"name": text(meta.get("name")), "cover_url": text(meta.get("cover_url"))})
    n = 0
    for s in songs:
        artists = [a for a in (getattr(s, "artists", None) or []) if isinstance(a, str)]
        emit("TRACK", {
            "id": text(getattr(s, "song_id", None)),
            "name": text(getattr(s, "name", None)),
            "artist": artists[0] if artists else text(getattr(s, "artist", None)),
            "album": text(getattr(s, "album_name", None)),
            "duration": getattr(s, "duration", None) or 0,
            "isrc": text(getattr(s, "isrc", None)),
            "cover_url": text(getattr(s, "cover_url", None)),
            "album_id": text(getattr(s, "album_id", None)),
            "artist_id": text(getattr(s, "artist_id", None)),
        })
        n += 1
    emit("END", {"count": n})
    return 0


sys.exit(main(sys.argv[1]))
