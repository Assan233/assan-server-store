"""Update only the title tag on the temporary file descriptor provided by Go."""

import os
import sys

from mutagen import File, MutagenError
from mutagen.asf import ASF
from mutagen.flac import FLAC
from mutagen.id3 import TIT2
from mutagen.monkeysaudio import MonkeysAudio
from mutagen.mp3 import MP3
from mutagen.mp4 import MP4
from mutagen.oggflac import OggFLAC
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis


def set_title(file, kind, title):
    if kind == "ogg":
        audio = File(file, options=[OggVorbis, OggOpus, OggFLAC])
        if audio is None:
            raise ValueError("unsupported Ogg stream")
    else:
        audio = {
            "mp3": MP3, "flac": FLAC, "mp4": MP4,
            "opus": OggOpus, "asf": ASF, "ape": MonkeysAudio,
        }[kind](file)

    if isinstance(audio, MP4) and not audio.info.channels:
        raise ValueError("no audio track")
    if audio.tags is None:
        audio.add_tags()
    tags = audio.tags
    if kind == "mp3":
        frames = tags.getall("TIT2")
        if len(frames) == 1 and frames[0].text == [title]:
            return False
        version = 3 if tags.version[:2] == (2, 3) else 4
        tags.setall("TIT2", [TIT2(encoding=3, text=[title])])
        if version == 3:
            tags.update_to_v23()
        audio.save(file, v2_version=version)
    else:
        key = {"mp4": "\xa9nam", "asf": "Title", "ape": "Title"}.get(kind, "title")
        # ASF extended descriptions may store a lowercase title alongside the
        # canonical Content Description field. Remove both to avoid stale titles.
        aliases = [name for name in tags.keys() if name.casefold() == "title"] if kind == "asf" else [key]
        old = tags.get(key, []) if aliases == [key] else []
        if ([str(item) for item in old] if isinstance(old, list) else [str(old)]) == [title]:
            return False
        if kind == "asf":
            for name in aliases:
                del tags[name]
        tags[key] = title if kind in ("asf", "ape") else [title]
        file.seek(0)
        audio.save(file)
    return True


try:
    with os.fdopen(3, "r+b") as file:
        changed = set_title(file, *sys.argv[1:])
        file.flush()
except OSError:
    sys.exit(3)
except (MutagenError, ValueError):
    sys.exit(2)

print("changed" if changed else "unchanged")
