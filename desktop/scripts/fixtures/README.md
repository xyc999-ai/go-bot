# Video test fixture

`playback.webm` is a generated four-second VP8 test pattern (320×180, 12 fps),
with no personal or external media. Regenerate with:

```sh
ffmpeg -f lavfi -i 'testsrc2=size=320x180:rate=12:duration=4' -an -c:v libvpx -b:v 90k playback.webm
```

The fixture is for automated decode/seek/download tests and is excluded from
packaged applications along with the scripts directory.
