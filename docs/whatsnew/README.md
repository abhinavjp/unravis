# What's new and release notes

Each release has plain-word notes that the app shows on the Updates page and in the "What's new" window,
and that the GitHub release page repeats under "Highlights". They come from small note files that travel
with the pull requests, so the words are written while the change is fresh.

## For a pull request that people can see

Add one file, `docs/whatsnew/fragments/<short-name>.json` (lower case, digits and dashes):

```json
{
  "schema": 1,
  "headline": "One plain line for the whole release (optional)",
  "highlights": [
    {
      "label": "Charts",
      "text": "Charts now explain what happened in plain words.",
      "alt": "The charts screen with the new summary line",
      "image": { "screen": "p9-blackbox-list" }
    }
  ],
  "changes": ["Extra lines for the full change list (optional)"]
}
```

Write for someone who has never seen the app: no jargon, no ids, short sentences.
A change nobody can see (build, tests, refactor) needs no file. Say "no visible change" in the pull request.

### Kinds of note

| Fragment has | Shown as |
| --- | --- |
| only `text` | a short note |
| `image` | the words and a real screenshot |
| `image` and `before` | before and after, side by side |

A picture is `{ "screen": "<id from tools/screens/screens.json>" }` or `{ "steps": [ ... ] }` (the same step
language as `tools/screens`), with optional `clip` (a selector, to picture one element), `theme`
(`dark` or `light`) and `wait` (milliseconds). Or the text `"/whatsnew/<folder>/<file>"` to reuse a picture
that is already in the app. The full format is at the top of `tools/whatsnew/lib.mjs`.

"before" is taken from the previous release's program, so it always shows what people had until now.

## At release time

When development for a release is finished, run once (from any folder; it uses temporary worktrees and leaves yours alone):

    pwsh tools/release/Prepare-WhatsNew.ps1          # -DryRun first to see the version and the waiting notes

It:

1. works out the next version from the tags,
2. builds the program from the tip of `develop`, and the previous release's program when a note asks for a "before" picture,
3. takes the screenshots from the demo build into `web/public/whatsnew/<rcNN>/`,
4. writes the entry of the new version into `web/public/releases/notes.json` (the notes, plus the commit subjects since the
   last release as the full change list) and deletes the used note files,
5. commits only those paths and opens ONE pull request into `develop` (`-NoPush` keeps it local).

The notes file and the pictures are inside the program, so the signed update carries them and the signature covers them.
The signed update list, the keys and the signing scripts are not touched.

`Promote-Develop.ps1` checks, read only, that no note file is still waiting on the commit it promotes. If one is, it stops and
says to run Prepare-WhatsNew first (`-SkipNotesCheck` passes anyway). No waiting notes (nothing visible, or already prepared) is fine.

`.github/workflows/release.yml` then builds the GitHub release notes with `tools/release/New-ReleaseNotes.ps1`, which puts the same
highlights (words and pictures) in front of the full change list.

Check the result: the headline and highlights are the words people read, so read them once; the commit-subject lines in the full list
are only filtered, not rewritten.

## Checks

- `node tools/ci/test-whatsnew.mjs` runs on every pull request.
- `pwsh tools/release/Test-Release.ps1` covers the release page.
