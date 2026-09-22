`variable-bitrate.mp3` is a synthetic four-second tone, with two seconds of
silence followed by mixed tones. It deliberately has no Xing duration index,
so duration and seeking must come from parsing frames rather than estimating
from the first frame's bitrate. Regenerate with:

```sh
ffmpeg -f lavfi -i 'aevalsrc=if(lt(t\,2)\,0\,0.2*sin(2*PI*440*t)+0.1*sin(2*PI*9000*t)):s=44100:d=4' -codec:a libmp3lame -q:a 2 -write_xing 0 -y variable-bitrate.mp3
```

`chirp.mp3` is a 60-second chirp at 300 + 20t Hz, so the pitch of any window
says which moment of the file it is. Seven-second blocks alternate between
loud with an 11025 Hz side tone and quiet, which swings the VBR bitrate; with
no Xing index its bitrate suggests 53.6 seconds. Regenerate with:

```sh
ffmpeg -f lavfi -i "aevalsrc='if(lt(mod(t,14),7),0.5,0.08)*sin(2*PI*(300*t+10*t*t))+if(lt(mod(t,14),7),0.15*sin(2*PI*11025*t),0)':s=44100" -t 60 -c:a libmp3lame -q:a 6 -write_xing 0 -metadata title=chirp -y chirp.mp3
```

The WAV fixtures are generated inside the tests using AVAudioFile.
