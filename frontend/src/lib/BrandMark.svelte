<script lang="ts">
  // The LiteAPI application icon, for use inside the app.
  //
  // This is the same mark as build/appicon.svg — the one that ships in the .app
  // bundle, the DMG and the NSIS installer. It is duplicated here rather than
  // imported because build/ is Wails' packaging directory and is not part of the
  // Vite source root; nothing under src/ can reach it. If the mark changes,
  // BOTH files have to change, and this comment is the only thing that says so.
  //
  // Inline SVG rather than <img src>: no second request, crisp at any size and
  // in any pixel ratio, and the 872 KB appicon.png stays out of the bundle.
  //
  // TWO ELEMENTS FROM appicon.svg ARE DELIBERATELY ABSENT. At 1024 the mark also
  // carries a faint inner border (stroke-width 18, 10% white) and a small plus
  // inside the node (stroke-width 22). The icon is drawn at 38px in the sidebar,
  // a scale of 0.037 — those strokes land at 0.67px and 0.82px, below one
  // device pixel on a non-retina display. They do not read as detail at that
  // size; they read as grey haze on the edge and a smudge in the middle. The
  // brackets (2.7px), the signal stroke (2.3px) and the node ring (1.3px) all
  // survive the reduction, and they are what makes the mark recognisable.
  //
  // The colours are fixed rather than themed. This is a logo: it should look the
  // same in Nord and Catppuccin Mocha as it does in the default themes, the way
  // an app icon in the dock does not restyle itself per wallpaper.
  type Props = {
    /** Rendered edge length in px. The sidebar uses the 38px default. */
    size?: number
    /** Announced name. Override where surrounding text already says "LiteAPI". */
    label?: string
  }

  let { size = 38, label = 'LiteAPI' }: Props = $props()
</script>

<svg
  class="brand-mark-svg"
  width={size}
  height={size}
  viewBox="0 0 1024 1024"
  xmlns="http://www.w3.org/2000/svg"
  role="img"
  aria-label={label}
>
  <defs>
    <linearGradient id="brand-mark-bg" x1="120" y1="80" x2="900" y2="960" gradientUnits="userSpaceOnUse">
      <stop stop-color="#1c2936" />
      <stop offset="1" stop-color="#0b121b" />
    </linearGradient>
    <linearGradient id="brand-mark-signal" x1="300" y1="250" x2="760" y2="760" gradientUnits="userSpaceOnUse">
      <stop stop-color="#ff9b58" />
      <stop offset="1" stop-color="#e85d2a" />
    </linearGradient>
  </defs>
  <rect width="1024" height="1024" rx="236" fill="url(#brand-mark-bg)" />
  <path
    d="M378 294 244 512l134 218"
    fill="none"
    stroke="#e8eff5"
    stroke-linecap="round"
    stroke-linejoin="round"
    stroke-width="74"
  />
  <path
    d="m646 294 134 218-134 218"
    fill="none"
    stroke="#e8eff5"
    stroke-linecap="round"
    stroke-linejoin="round"
    stroke-width="74"
  />
  <path
    d="M446 676 578 348"
    fill="none"
    stroke="url(#brand-mark-signal)"
    stroke-linecap="round"
    stroke-width="62"
  />
  <circle cx="512" cy="512" r="78" fill="#111d29" stroke="url(#brand-mark-signal)" stroke-width="34" />
</svg>

<style>
  /* The wrapper owns the box; the mark owns its own rounded background, so it
     must fill that box exactly rather than sit inside it with a seam. */
  .brand-mark-svg {
    display: block;
  }
</style>
