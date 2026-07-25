# Native macOS Menu Defect Loop — 2026-07-21

## Scope

Priority defect 1 from the completion goal: make the packaged LiteAPI application menu follow macOS conventions and remove Settings from File.

## Baseline reproduction

Independent native QA attached to the packaged Wails app and found:

- LiteAPI application menu: Hide, Hide Others, Show All, Quit only.
- File menu: Settings/Preferences incorrectly present.
- About, Settings, and Services absent from the application menu.

Classification: **P1**.

## First bounded implementation

The first slice enabled Wails’ native About configuration, removed Settings from File, and augmented the generated AppKit application menu with Settings and a real `NSApp.servicesMenu`. Static checks and a packaged build passed.

Independent QA on signed executable `ea3f5ce42d45d548bb55351c864c179561c5862cb40c7519d77660bd2e7649ae` confirmed the correct visible menu, About dialog, Services population, File cleanup, and the `⌘,` path. It also reproduced a new P1 twice: clicking the Cocoa-created Settings item terminated LiteAPI, while `⌘,` remained healthy.

## Root review and repair plan

Root accepted the liveness evidence and rejected the direct Cocoa-to-exported-Go callback. The repair reused a normal Wails `menu.Text` item with the existing `open-preferences` callback. The macOS augmentation now moves that exact native Wails menu item from its cross-platform fallback location into the application menu, preserving Wails’ target/action and accelerator. Installation is idempotent and does not synthesize keyboard input.

## Exact-package retest

Root rebuilt and ad-hoc signed:

- App: `/Users/mostafi/Developer/Workspace/lite_api/build/bin/LiteAPI.app`
- Architecture: `arm64`
- Bundle identifier: `com.wails.LiteAPI`
- Retested executable SHA-256: `7b6e2d3161f7d991daff6b211f5f0e259b7a12ee9bc8e62a672edb5341cd6783`
- `codesign --verify --deep --strict`: pass

The same independent native QA agent then passed:

- Application-menu AX order: About LiteAPI; Settings…; Services; Hide LiteAPI; Hide Others; Show All; Quit LiteAPI.
- Services opened as a real populated system submenu.
- File contained New/Open/Import/Save/Close/Reopen actions and no Settings/Preferences.
- Help contained Capabilities, Keyboard Shortcuts, and LiteAPI Help, with no duplicated Settings.
- Application-menu Settings opened exactly one Preferences view and left `isRunning:true` in two clean launch/click checks.
- `⌘,` focused the same single Preferences view and left the app running.
- About opened a native alert and dismissed back to a responsive shell.
- No crash, panic, fatal, or diagnostic AX text was found.

Screenshots:

- Settings run 1: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-21 at 11.21.12 PM.jpeg`
- Settings run 2: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-21 at 11.21.36 PM.jpeg`
- `⌘,`: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-21 at 11.21.42 PM.jpeg`
- About: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-21 at 11.22.20 PM.jpeg`
- Responsive after About: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-21 at 11.22.26 PM.jpeg`

## Escape observation

Computer Use did not dismiss an open LiteAPI native menu when it injected Escape. The same method failed identically on Yaak’s native application menu; each app’s exposed AX `Cancel` action dismissed the menu. Independent QA therefore retracted the product defect and classified this as a Computer Use key-delivery limitation, not LiteAPI behavior.

## Verdict

**Priority menu defect loop: PASS at the retested slice hash.** The final release package must repeat this workflow during each of the three clean-state acceptance runs because later implementation will change the executable hash.

