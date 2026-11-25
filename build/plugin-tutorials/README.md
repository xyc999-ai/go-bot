# Plugin tutorial media

Capture the real vendor console with the approved browser/computer tool. Store
raw screenshots and the JSON editing recipe outside Git, for example in `.cache`.
Never publish browser tabs, account identifiers, keys, tokens or private project
names. The renderer removes only explicitly reviewed regions and overlays focus
and captions; it does not invent vendor interface states.

Each scene in the private recipe has `image`, `crop: [left,top,right,bottom]`,
`redactions: [[left,top,right,bottom], ...]`, `focus`, optional `zoom` / `seconds`,
and `captions: {"en": [title, body], "zh-CN": [title, body]}`.

```sh
python3 build/plugin-tutorials/render.py .cache/tutorial-recipe.json \
  --language en --font /path/to/a/unicode-font.ttf \
  --output ../go-bot-plugins/plugins/x/assets/account-setup.en.mp4
```

Requires Pillow and ffmpeg with libx264. Review extracted full-page and zoomed
frames, decode the full result, and verify playback through Controller and the
mobile player. Only then add the MP4/poster paths to `plugin.json.tutorials`.
The project keeps the X tutorial before the Save action, by user instruction.

The Google Workspace tutorial records the real API library, branding, audience,
test-user form and Desktop-client form without submitting Cloud configuration.
The last scene shows the current console restriction on downloading old secrets;
it reminds users to keep the JSON at creation time. It does not fabricate a
post-creation download screen or generate another application for the recording.

Business plugin sources and published tutorial assets live in the sibling [plugin repository](https://github.com/xyc999-ai/go-bot-plugins). Run the capture helper from the main repository and select that output path.
