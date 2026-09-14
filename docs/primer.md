# Primer Design Specification for Markspace

This document defines how Markspace reproduces GitHub's Primer product UI in plain HTML, CSS and small ES modules. Primer is GitHub's design system. Its React components cannot run in Markspace, so every component below is specified as markup, CSS values and behaviour. Every value comes from Primer's documentation, the `@primer/primitives` package or the Primer React source, unless a row says otherwise.

Two rules apply throughout.

1. Markspace follows Primer everywhere, with a fuchsia accent built from Primer's own `pink` base scale instead of Primer's blue accent and green primary button. Section 2.4 specifies it.
2. Where Primer and an earlier Markspace rule differ, Markspace follows Primer. A `ui.md` rule that Primer does not cover stays in force. Section 10 lists the rule for each such topic.

The document has these sections.

1. Sources
2. Tokens
3. Typography, spacing, radius, shadow, motion and breakpoints
4. Icons
5. Components
6. Patterns
7. Narrow viewports
8. Editor on phones
9. Accessibility
10. Rules in force
11. Implementation map

## 1. Sources

The pages below were current on 2026-09-13.

### 1.1 Packages

| Package | Version | Licence | Use |
| --- | --- | --- | --- |
| `@primer/primitives` | 11.10.0 | MIT | Token CSS, base colour scales in `dist/figma/scales`, contrast rules in `scripts/colorContrast.config.ts` on `main` |
| `@primer/octicons` | 19.36.0 | MIT | Icons |
| `@primer/react` | 38.39.0 | MIT | Compiled component CSS in `dist/<Component>/*.css`, read for pixel values. Not vendored |
| `@primer/css` | 22.3.1 on npm | MIT | Focus mixins in `src/base/base.scss` and `src/support/mixins/misc.scss` on `main`, the iOS input size rule in `src/forms/form-control.scss`, and `dist/markdown.css` and `dist/box.css` from the package. Not vendored. Primer CSS is in maintenance |

### 1.2 Foundations

| Page | URL |
| --- | --- |
| Foundations index | https://primer.style/product/getting-started/foundations/ |
| Colour usage | https://primer.style/product/getting-started/foundations/color-usage/ |
| Typography | https://primer.style/product/getting-started/foundations/typography/ |
| Icons | https://primer.style/product/getting-started/foundations/icons/ |
| Layout | https://primer.style/product/getting-started/foundations/layout/ |
| Responsive | https://primer.style/product/getting-started/foundations/responsive/ |
| Content | https://primer.style/product/getting-started/foundations/content/ |
| Primitives, with its `color/`, `size/`, `typography/` and `token-names/` pages | https://primer.style/product/primitives/ |
| React theming | https://primer.style/product/getting-started/react/theming/ |
| Octicons | https://primer.style/octicons/ |

### 1.3 Accessibility

- https://primer.style/accessibility/design-guidance/color-considerations/
- https://primer.style/accessibility/design-guidance/focus-management/
- https://primer.style/accessibility/design-guidance/links-and-buttons/
- https://primer.style/accessibility/design-guidance/motion-and-animation/
- https://primer.style/accessibility/design-guidance/text-resize-and-spacing/
- https://primer.style/accessibility/design-guidance/semantic-html-and-aria/
- https://primer.style/accessibility/design-guidance/app-like-web-experiences/
- https://primer.style/accessibility/patterns/accessible-notifications-and-messages/
- https://primer.style/accessibility/patterns/keyboard-shortcuts/
- https://primer.style/accessibility/patterns/primer-components/icons/
- https://primer.style/accessibility/patterns/primer-components/tooltips/
- https://primer.style/accessibility/patterns/placeholders/
- https://primer.style/accessibility/tools-and-resources/checklists/designer-checklist/
- https://primer.style/accessibility/tools-and-resources/checklists/mobile-checklist/

### 1.4 UI Patterns

- https://primer.style/product/ui-patterns/ and its `saving`, `notification-messaging`, `loading`, `empty-states`, `progressive-disclosure`, `forms`, `navigation` and `degraded-experiences` pages

Primer has no pattern page for data tables, destructive confirmation or keyboard shortcuts. Those rules come from the DataTable, ConfirmationDialog and ActionList component guidance and the keyboard shortcuts accessibility page. Primer has no Toast component, and `/product/components/toast/` returns a not-found page.

### 1.5 Components

Each component has an overview page at `https://primer.style/product/components/<slug>/`, and most have guidance and accessibility sub-pages. This document draws on the pages of the components below.

- `button`, `icon-button`, `button-group`, `action-list`, `action-menu`, `select-panel`, `segmented-control`
- `dialog`, `confirmation-dialog`, `overlay`, `tooltip`, `popover`, `banner`, `inline-message`
- `label`, `state-label`, `counter-label`, `token`, `avatar`, `avatar-stack`
- `underline-nav`, `underline-panels`, `nav-list` (its guidance page sits at a misspelled path, `nav-list/guidlines/`), `tree-view`, `page-header`, `page-layout`, `split-page-layout`, `breadcrumbs`, `pagination`
- `text-input`, `textarea`, `select`, `checkbox`, `checkbox-group`, `radio`, `radio-group`, `toggle-switch`, `form-control`, `autocomplete`
- `blankslate`, `progress-bar`, `spinner`, `skeleton-box`, `timeline`, `relative-time`, `truncate`, `data-table`, `details`

Layout, navigation, form and feedback pixel values come from `primer/react` `main` at commit `6c645987` (2026-09-11, package version 38.39.0), in `packages/react/src/<Component>/<Component>.module.css` and the PostCSS mixins `activeIndicatorLine` and `focusOutline`. RelativeTime wraps `@github/relative-time-element` 5.3.1 (MIT).

The sub-pages in this table return a not-found page.

| Component | Missing sub-pages |
| --- | --- |
| Flash | all, including the overview |
| Overlay | guidelines |
| InlineMessage | guidelines, accessibility |
| Label | guidelines |
| StateLabel | guidelines |
| CounterLabel | guidelines, accessibility |
| Avatar | guidelines, accessibility |
| AvatarStack | guidelines, accessibility |

Flash is deprecated. `Flash.d.ts` in `@primer/react` 38.39.0 says "Use Banner instead".

### 1.6 Source Code

- https://raw.githubusercontent.com/primer/primitives/main/scripts/colorContrast.config.ts
- https://raw.githubusercontent.com/primer/css/main/src/base/base.scss
- https://raw.githubusercontent.com/primer/css/main/src/support/mixins/misc.scss
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/Button/ButtonBase.module.css
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/PageLayout/PageLayout.module.css
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/Dialog/Dialog.module.css
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/Overlay/Overlay.module.css
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/ActionMenu/ActionMenu.module.css
- https://raw.githubusercontent.com/primer/react/main/packages/react/src/SelectPanel/SelectPanel.module.css
- https://registry.npmjs.org/@primer/react/-/react-38.39.0.tgz, compiled CSS for every component in section 5

Where a primer.style page and the compiled CSS disagree, this document follows the CSS for pixel values and the page for behaviour.

## 2. Tokens

A design token is a named value, such as a colour or a size, published as a CSS custom property. Primer publishes two layers. Base tokens hold raw scales, such as `--base-size-16`. Functional tokens name a purpose, such as `--fgColor-muted` for secondary text. Markspace CSS uses functional tokens only. It uses a base token only where no functional token exists, and the rows below name each case.

### 2.1 Files to Vendor

All files come from `@primer/primitives` 11.10.0, directory `dist/css`, licensed MIT, copyright GitHub Inc. They are plain CSS and need no build step.

| Source file | Bytes | Content |
| --- | --- | --- |
| `base/size/size.css` | 1000 | `--base-size-2` to `--base-size-128` |
| `base/size/z-index.css` | 188 | `--base-zIndex-*` |
| `base/typography/typography.css` | 1675 | `--base-text-size-*`, `--base-text-weight-*`, `--base-text-lineHeight-*` |
| `base/motion/motion.css` | 1110 | `--base-duration-*`, `--base-easing-*` |
| `functional/size/border.css` | 1578 | `--borderWidth-*`, `--focus-outline-width`, `--focus-outline-offset`, `--boxShadow-*` |
| `functional/size/breakpoints.css` | 194 | `--breakpoint-*` |
| `functional/size/radius.css` | 713 | `--borderRadius-*` |
| `functional/size/size.css` | 2622 | `--control-*`, `--controlStack-*`, `--overlay-*`, `--spinner-*`, `--stack-*` |
| `functional/size/size-coarse.css` | 526 | `--control-minTarget-auto` under `(pointer: coarse)` |
| `functional/size/size-fine.css` | 514 | `--control-minTarget-auto` under `(pointer: fine)` |
| `functional/size/z-index.css` | 729 | `--zIndex-*` |
| `functional/spacing/space.css` | 686 | `--space-xxs` to `--space-xl` |
| `functional/typography/typography.css` | 5690 | `--fontStack-*`, `--text-*` |
| `functional/motion/motion.css` | 1996 | `--motion-*` |
| `functional/themes/light.css` | 121339 | every colour and shadow token for the `light` theme |
| `functional/themes/dark.css` | 121122 | every colour and shadow token for the `dark` theme |

`primitives.css` (723 bytes) only `@import`s the first 14 files. `functional/size/viewport.css` (471 bytes) holds `@custom-media` rules. Browsers do not support `@custom-media`, so Markspace does not vendor that file and writes the media queries out in full (section 3.7).

The vendored files sit at these paths.

| Path in Markspace | Content |
| --- | --- |
| `internal/web/static/vendor/primer/primitives-11.10.0.css` | The 14 non-theme files above, concatenated unchanged in the `@import` order of `primitives.css`, 21 221 bytes |
| `internal/web/static/vendor/primer/light-11.10.0.css` | `functional/themes/light.css`, unchanged |
| `internal/web/static/vendor/primer/dark-11.10.0.css` | `functional/themes/dark.css`, unchanged |
| `internal/web/static/vendor/primer/LICENSE-primitives` | The package `LICENSE` |
| `internal/web/static/theme.css` | The Markspace theme override from section 2.4 |

Concatenation replaces 14 requests with one. The file content stays byte-identical to the package, so a version upgrade repeats the copy.

### 2.2 Loading Order

`base.html` `head` loads, in this order:

1. `/static/vendor/primer/primitives-11.10.0.css`
2. `/static/vendor/primer/light-11.10.0.css`
3. `/static/vendor/primer/dark-11.10.0.css`
4. `/static/theme.css`
5. `/static/css/base.css`
6. `/static/css/components.css`, `forms.css`, `pages.css`, `markdown.css`, `shell.css`
7. The area stylesheets `/static/css/usage.css`, `admin.css`, `files.css`, `panels.css` and `settings.css`
8. `/static/highlight.css`, which the server generates

`shell.html` adds `/static/css/editor.css` and `panels.css` after them. `sw.js` precaches every file in the list, so the offline shell renders with the same tokens.

### 2.3 Theme Switch

Primer selects a theme with three attributes on the root element. The theme files use these selectors.

| File | Selector | Applies when |
| --- | --- | --- |
| `light.css` | `[data-color-mode="light"][data-light-theme="light"]` | light mode is chosen |
| `light.css` | `[data-color-mode="auto"][data-light-theme="light"]` | system mode, before the media query below |
| `light.css` | `@media (prefers-color-scheme: dark) { [data-color-mode][data-color-mode="auto"][data-dark-theme="light"] }` | system mode while the system is dark and the dark slot holds the light theme. Markspace never sets this |
| `dark.css` | `[data-color-mode="dark"][data-dark-theme="dark"]` | dark mode is chosen |
| `dark.css` | `[data-color-mode="auto"][data-light-theme="dark"]` | system mode when the light slot holds the dark theme. Markspace never sets this |
| `dark.css` | `@media (prefers-color-scheme: dark) { [data-color-mode][data-color-mode="auto"][data-dark-theme="dark"] }` | system mode while the system is dark |

The media-query selector has three attribute selectors against two, so it wins over the `auto` light selector without depending on file order.

Markspace sets the attributes on `<html>`.

| Stored preference (`markspace.theme`) | `data-color-mode` | `data-light-theme` | `data-dark-theme` |
| --- | --- | --- | --- |
| none (system) | `auto` | `light` | `dark` |
| `light` | `light` | `light` | `dark` |
| `dark` | `dark` | `light` | `dark` |

- The server renders `data-light-theme="light" data-dark-theme="dark" data-color-mode="auto"` on `<html>` in `base.html` and `shell.html`.
- The inline head script in `base.html` changes only `data-color-mode`, before first paint. `saveThemePreference` in `shell.js` and the theme handler in `app.js` do the same.
- The account menu holds the choice as one icon-only SegmentedControl (5.5): `sun` for light, `moon` for dark and `device-desktop` for the system theme. The segments are named "Light theme", "Dark theme" and "Use system theme" and have Tooltips. A choice applies at once without a reload and saves `markspace.theme`. Other open tabs apply it through the `storage` event. `shell.js` handles the client shell and `app.js` handles server pages.
- `data-theme` and the `dark` class are removed. No stylesheet may test them. A rule that must differ by mode tests the Primer attributes, but colour differences always go through tokens.
- `prefers-color-scheme` needs no JavaScript listener in system mode. The `auto` selectors follow the system through CSS.
- The theme files do not set `color-scheme`, so `static/css/base.css` sets it. Native date pickers, scrollbars and form controls then match the theme.

```css
[data-color-mode="light"], [data-color-mode="auto"] { color-scheme: light; }
[data-color-mode="dark"] { color-scheme: dark; }
@media (prefers-color-scheme: dark) { [data-color-mode="auto"] { color-scheme: dark; } }
```

Other Primer themes exist. Markspace does not vendor them.

| Theme id | What it is |
| --- | --- |
| `light_high_contrast`, `dark_high_contrast` | Themes that meet 7:1 for text and 4.5:1 for borders instead of 4.5:1 and 3:1 |
| `dark_dimmed`, `dark_dimmed_high_contrast` | A dark theme with a grey rather than near-black background |
| `light_colorblind`, `dark_colorblind` and their high-contrast forms | Red and green replaced for protanopia and deuteranopia |
| `light_tritanopia`, `dark_tritanopia` and their high-contrast forms | Colours adjusted for tritanopia |

Each is a separate file of about 121 KB with the same selector scheme, for example `[data-color-mode="light"][data-light-theme="light_high_contrast"]`. Adding one later needs a matching block in `theme.css` that meets that theme's contrast levels.

### 2.4 The Fuchsia Accent

#### What the Accent Does in Primer

Primer's accent role is blue. Primer uses it for links, focus rings, selected and checked controls, the current-item bar in ActionList and NavList, the outline button, accent progress bars and text selection.

The primary button does not follow the accent. Primer's colour usage page assigns roles in a table. The `accent` role covers "Links, selected, active, and focus states, and neutral information". The `success` role covers "Primary buttons, positive messaging and successful states". In both theme files `--button-primary-bgColor-rest` is `var(--bgColor-success-emphasis)`, which is `#1f883d` in light and `#238636` in dark. In Markspace, buttons and every other interactive and brand use are fuchsia. Markspace overrides the primary button tokens with values from the pink scale. Success keeps green and marks status only, such as a success Banner or a "Saved" message.

The success, danger, severe, done, upsell, open and closed roles keep Primer's values. The attention role moves to Primer's `orange` scale, because Markspace uses no yellow or amber. The sponsors role and `--underlineNav-borderColor-active` follow the accent tokens. Neutral colours carry a small fuchsia tint. The subsections after the contrast check give these values.

#### Choice of Scale

Primer's base colour scales have ten steps, 0 to 9, in `dist/figma/scales/light.json` and `dark.json`. The Markspace fuchsia is `#a21caf` in light and `#e879f9` in dark. The table gives the OKLab distance (ΔE × 100, lower is closer) and hue of the nearest steps. OKLab is a colour space in which equal distances look about equally different.

| Candidate | Light step, hue, ΔE to #a21caf | Dark step, hue, ΔE to #e879f9 | Primer role that already uses it |
| --- | --- | --- | --- |
| `pink` | 5 `#bf3989`, 348°, 10.5 | 3 `#f778ba`, 350°, 9.7 | sponsors |
| `purple` | 5 `#8250df`, 295°, 11.9 | 3 `#be8fff`, 302°, 7.9 | done, upsell |
| target | `#a21caf`, 324° | `#e879f9`, 322° | none |

The two scales are equally close. Pink sits 24° to 28° on the red side of fuchsia, and purple 20° to 29° on the blue side. Markspace uses `pink`. `purple` would make the accent identical to Primer's done colour, which marks completed states such as a resolved thread. Markspace has no sponsors UI, so pink collides with nothing.

Primer's display scale `plum` is closer still (light step 6 `#961edc` ΔE 7.7, dark step 6 `#d07ef7` ΔE 4.1). Display scales are meant for labels and data colours, not functional roles, so Markspace does not use it for the accent.

| Step | Light `pink` | Dark `pink` |
| --- | --- | --- |
| 0 | `#ffeff7` | `#ffdaec` |
| 1 | `#ffd3eb` | `#ffbedd` |
| 2 | `#ffadda` | `#ff9bce` |
| 3 | `#ff80c8` | `#f778ba` |
| 4 | `#e85aad` | `#db61a2` |
| 5 | `#bf3989` | `#bf4b8a` |
| 6 | `#99286e` | `#9e3670` |
| 7 | `#772057` | `#7d2457` |
| 8 | `#611347` | `#5e103e` |
| 9 | `#4d0336` | `#42062a` |

#### Token Values

The table gives the `blue` scale step and alpha of each Primer accent token. The pink token takes the same step and alpha. Where Primer uses a value between two steps, the table names the step used.

| Token | Primer light | Markspace light | Primer dark | Markspace dark |
| --- | --- | --- | --- | --- |
| `--fgColor-accent` | `#0969da` blue 5 | `#bf3989` pink 5 | `#4493f8` between blue 3 and 4 | `#f778ba` pink 3 |
| `--bgColor-accent-emphasis` | `#0969da` blue 5 | `#bf3989` pink 5 | `#1f6feb` blue 5 | `#bf4b8a` pink 5 |
| `--bgColor-accent-muted` | `#ddf4ff` blue 0 | `#ffeff7` pink 0 | `#388bfd1a` blue 4 at 10% | `#db61a21a` pink 4 at 10% |
| `--borderColor-accent-emphasis` | `#0969da` blue 5 | `#bf3989` pink 5 | `#1f6feb` blue 5 | `#bf4b8a` pink 5 |
| `--borderColor-accent-muted` | `#54aeff66` blue 3 at 40% | `#ff80c866` pink 3 at 40% | `#388bfd66` blue 4 at 40% | `#db61a266` pink 4 at 40% |
| `--selection-bgColor` | `#0969da33` | `#bf398933` | `#1f6febb3` | `#bf4b8ab3` |
| `--control-checked-bgColor-hover` | `#0860ca` between blue 5 and 6 | `#99286e` pink 6 | `#2a7aef` between blue 5 and 4 | `#db61a2` pink 4 |
| `--control-checked-bgColor-active` | `#0757ba` near blue 6 | `#99286e` pink 6 | `#3685f3` near blue 4 | `#db61a2` pink 4 |
| `--button-outline-bgColor-active` | `#0757ba` near blue 6 | `#99286e` pink 6 | `#0d419d` blue 7 | `#7d2457` pink 7 |
| `--button-outline-fgColor-rest` | `var(--fgColor-accent)` | follows | `#388bfd` blue 4 | `#db61a2` pink 4 |
| `--button-outline-fgColor-hover` | `#ffffff` | unchanged | `#58a6ff` blue 3 | `#f778ba` pink 3 |
| `--button-outline-fgColor-disabled` | `#0969da80` | `#bf398980` | `#4493f880` | `#f778ba80` |
| `--button-outline-shadow-selected` | `inset 0 1px 0 0 #00215533` blue 9 | `inset 0 1px 0 0 #4d033633` pink 9 | `0 0 0 0 #00000000` | unchanged |
| `--buttonCounter-outline-fgColor-rest` | `#0550ae` blue 6 | `#99286e` pink 6 | `#388bfd` blue 4 | `#db61a2` pink 4 |
| `--buttonCounter-outline-fgColor-hover` | not set | not set | `#58a6ff` blue 3 | `#f778ba` pink 3 |
| `--buttonCounter-outline-fgColor-disabled` | `#0969da80` | `#bf398980` | `#4493f880` | `#f778ba80` |
| `--buttonCounter-outline-bgColor-rest` | `#0969da1a` | `#bf39891a` | `#051d4d33` blue 9 | `#42062a33` pink 9 |
| `--buttonCounter-outline-bgColor-hover` | not set | not set | `#051d4d33` blue 9 | `#42062a33` pink 9 |
| `--buttonCounter-outline-bgColor-disabled` | `#0969da0d` | `#bf39890d` | `#1f6feb0d` | `#bf4b8a0d` |
| `--selectMenu-bgColor-active` | `#b6e3ff` blue 1 | `#ffd3eb` pink 1 | `#0c2d6b` blue 8 | `#5e103e` pink 8 |
| `--treeViewItem-leadingVisual-iconColor-rest` | `#54aeff` blue 3 | `#ff80c8` pink 3 | `#9198a1` neutral | unchanged |
| `--reactionButton-selected-bgColor-rest` | `#ddf4ff` blue 0 | `#ffeff7` pink 0 | `#388bfd33` | `#db61a233` |
| `--reactionButton-selected-bgColor-hover` | `#caecff` between blue 0 and 1 | `#ffd3eb` pink 1 | not set | not set |
| `--reactionButton-selected-fgColor-hover` | `#0550ae` blue 6 | `#99286e` pink 6 | `#79c0ff` blue 2 | `#ff9bce` pink 2 |

These tokens are `var()` references to the tokens above in both theme files. They follow the override with no entry of their own: `--fgColor-link`, `--focus-outline-color`, `--focus-outlineColor`, `--focus-outline`, `--border-accent-emphasis`, `--border-accent-muted`, `--control-checked-bgColor-rest`, `--progressBar-bgColor-accent`, `--codeMirror-selection-bgColor`, `--diffBlob-hunkLine-bgColor`, `--diffBlob-hunkNum-bgColor-hover`, and in light `--button-outline-bgColor-hover`.

`theme.css` repeats Primer's selectors, so it applies in the same cases and wins by load order. It has four blocks, one per selector group of the two theme files.

```css
/* Markspace theme on @primer/primitives 11.10.0. */
[data-color-mode="light"][data-light-theme="light"],
[data-color-mode="auto"][data-light-theme="light"] {
  --fgColor-accent: #bf3989;
  --bgColor-accent-emphasis: #bf3989;
  /* every light value from the table */
}
@media (prefers-color-scheme: dark) {
  [data-color-mode][data-color-mode="auto"][data-dark-theme="light"] {
    /* every light value from the table, repeated */
  }
}
[data-color-mode="dark"][data-dark-theme="dark"],
[data-color-mode="auto"][data-light-theme="dark"] {
  --fgColor-accent: #f778ba;
  /* every dark value from the table */
}
@media (prefers-color-scheme: dark) {
  [data-color-mode][data-color-mode="auto"][data-dark-theme="dark"] {
    --fgColor-accent: #f778ba;
    /* every dark value from the table, repeated */
  }
}
```

#### Contrast Check

The minimums come from `scripts/colorContrast.config.ts` in `primer/primitives`. That file requires 4.5:1 for text pairs and 3:1 for border pairs in the light and dark themes. It does not list the focus colour. For the focus colour, Markspace applies WCAG 2.2 criterion 1.4.11 (non-text contrast, 3:1). Ratios are rounded down to two decimals, as Primer's script rounds them. Translucent colours are composited over the named background first.

| Pair | Required | Light | Dark |
| --- | --- | --- | --- |
| `--fgColor-accent` on `--bgColor-default` | 4.5 | 5.04 | 7.52 |
| `--fgColor-accent` on `--bgColor-muted` | 4.5 | 4.73 | 6.88 |
| `--fgColor-accent` on `--bgColor-inset` | 4.5 | 4.73 | 8.16 |
| `--fgColor-accent` on `--bgColor-accent-muted` | 4.5 | 4.55 | 6.76 |
| `--fgColor-default` on `--bgColor-accent-muted` | 4.5 | 14.24 | 15.63 |
| `--fgColor-onEmphasis` on `--bgColor-accent-emphasis` | 4.5 | 5.04 | 4.57 |
| `--focus-outlineColor` on `--bgColor-default` | 3 | 5.04 | 4.13 |
| `--focus-outlineColor` on `--bgColor-muted` | 3 | 4.73 | 3.78 |
| `--focus-outlineColor` on `--control-bgColor-rest` | 3 | 4.73 | 3.25 |
| `--fgColor-onEmphasis` on `--control-checked-bgColor-active` (check mark, non-text) | 3 | 7.28 | 3.36 |
| `--fgColor-default` on `--selection-bgColor` | 4.5 | 11.77 | 6.68 |

Every pair passes. Two other steps pass but are not used. Dark `pink 4` as `--fgColor-accent` passes (5.62 on default) but is farther from `#e879f9` than `pink 3`. Dark `pink 6` as `--bgColor-accent-emphasis` passes with white text (6.53) but reverses Primer's dark rule that emphasis sits at step 5.

#### Primary Button

The table places each green value on Primer's green scale by OKLab lightness. The pink value sits at the same offset from the rest step on the pink scale, mixed in OKLab between the two nearest steps. Light rest keeps green's position, between pink 4 and 5. Dark rest starts at pink 6. Primer's dark hover and active values are lighter than rest, and white text on dark pink 5 reaches only 4.57:1, so a lighter start fails. `--button-primary-fgColor-rest` and `--button-primary-iconColor-rest` stay white. `--button-primary-borderColor-rest` stays `var(--borderColor-translucent)`, which the neutral tint changes.

| Token | Primer light | Markspace light | Primer dark | Markspace dark |
| --- | --- | --- | --- | --- |
| `--button-primary-bgColor-rest` | `#1f883d` green 4.75 | `#c94192` pink 4.75 | `#238636` green 5.0 | `#9e3670` pink 6 |
| `--button-primary-bgColor-hover` | `#1c8139` green 4.94 | `#c13b8b` pink 4.94 | `#29903b` green 4.61 | `#ab3e7a` pink 5.61 |
| `--button-primary-bgColor-active` | `#197935` green 5.21 | `#b73583` pink 5.21 | `#2e9a40` green 4.22 | `#b84684` pink 5.22 |
| `--button-primary-bgColor-disabled` | `#95d8a6` green 1.85 | `#ffb3dd` pink 1.85 | `#105823` green 6.8 | `#641443` pink 7.80 |
| `--button-primary-borderColor-disabled` | `var(--button-primary-bgColor-disabled)` | follows | `#105823` | `var(--button-primary-bgColor-disabled)` |
| `--button-primary-shadow-selected` | `inset 0 1px 0 0 #002d114d` green 9 | `inset 0 1px 0 0 #4d03364d` pink 9 | `0 0 0 0 #00000000` | unchanged |

#### Attention and Highlight

Attention takes the orange step and alpha of the yellow step Primer uses. `--highlight-neutral-bgColor` marks search matches and uses orange 1 in light and orange 3 at 40% in dark.

Attention stays distinct from danger. In light, orange 5 has an OKLCH hue of 45° and chroma 0.16, and `--fgColor-danger` `#d1242f` has hue 25° and chroma 0.21. In dark, orange 3 has hue 53° and chroma 0.15, and `#f85149` has hue 27° and chroma 0.20. Attention messages also carry the `alert` icon and text, never colour alone.

| Token | Primer light | Markspace light | Primer dark | Markspace dark |
| --- | --- | --- | --- | --- |
| `--fgColor-attention` | `#9a6700` | `#bc4c00` orange 5 | `#d29922` | `#f0883e` orange 3 |
| `--bgColor-attention-emphasis` | `#9a6700` | `#bc4c00` orange 5 | `#9e6a03` | `#bd561d` orange 5 |
| `--bgColor-attention-muted` | `#fff8c5` | `#fff1e5` orange 0 | `#bb800926` | `#db6d2826` orange 4 at 15% |
| `--borderColor-attention-emphasis` | `#9a6700` | `#bc4c00` orange 5 | `#9e6a03` | `#bd561d` orange 5 |
| `--borderColor-attention-muted` | `#d4a72c66` | `#fb8f4466` orange 3 at 40% | `#bb800966` | `#db6d2866` orange 4 at 40% |
| `--highlight-neutral-bgColor` | `#fff8c5` | `#ffd8b5` orange 1 | `#d2992266` | `#f0883e66` orange 3 at 40% |

#### Brand Uses That Follow the Accent

These tokens are set to `var()` references in both themes, so they take the accent values above.

| Token | Primer light | Primer dark | Markspace |
| --- | --- | --- | --- |
| `--underlineNav-borderColor-active` | `#fd8c73` | `#f78166` | `var(--borderColor-accent-emphasis)` |
| `--fgColor-sponsors` | `#bf3989` | `#db61a2` | `var(--fgColor-accent)` |
| `--bgColor-sponsors-emphasis` | `#bf3989` | `#bf4b8a` | `var(--bgColor-accent-emphasis)` |
| `--bgColor-sponsors-muted` | `#ffeff7` | `#db61a21a` | `var(--bgColor-accent-muted)` |
| `--borderColor-sponsors-emphasis` | `#bf3989` | `#bf4b8a` | `var(--borderColor-accent-emphasis)` |
| `--borderColor-sponsors-muted` | `#ff80c866` | `#db61a266` | `var(--borderColor-accent-muted)` |

Selected states, the checked Checkbox, Radio and ToggleSwitch, the focus outline, links, the text selection and Label accent already use the accent tokens in Primer, so they follow the pink values with no entry of their own.

#### Neutral Tint

Each tinted value keeps Primer's OKLCH lightness and alpha. Its hue becomes 330°. Its chroma becomes the larger of Primer's chroma and 0.005 + 0.007 × sin(π × L), where L is the OKLCH lightness. That gives about 0.005 near white and black and 0.012 at middle lightness. sRGB white holds no chroma, so a white value steps down 0.004 in lightness to `#fffdff`. The tint covers the literal values of `--bgColor-default`, `-muted`, `-inset`, `-emphasis`, `-neutral-muted`, `-neutral-emphasis` and `-disabled`, the matching `--borderColor-*` tokens and `--borderColor-translucent`, `--fgColor-default`, `-muted`, `-disabled` and `-neutral`, the `--control-*` tokens except checked and danger, `--controlTrack-*`, `--controlKnob-*` except checked, the overlay colours, `--button-default-*`, `--button-invisible-*`, `--button-inactive-*`, `--tooltip-bgColor`, `--skeletonLoader-bgColor`, `--timelineBadge-bgColor`, `--counter-*` and `--avatarStack-fade-*`. `--fgColor-onEmphasis` and the white and black tokens are not tinted. Fully transparent values and values that are `var()` references in the theme file are skipped. A reference follows its tinted target.

| Token | Primer light | Markspace light | Primer dark | Markspace dark |
| --- | --- | --- | --- | --- |
| `--avatarStack-fade-bgColor-default` | `#c8d1da` | `#d6ccd5` | `#3d444d` | `#494048` |
| `--avatarStack-fade-bgColor-muted` | `#dae0e7` | `#e4dde3` | `#2a313c` | `#372d36` |
| `--bgColor-default` | `#ffffff` | `#fffdff` | `#0d1117` | `#140f14` |
| `--bgColor-disabled` | `#eff2f5` | `#f4f0f4` | `#212830` | `#2d242c` |
| `--bgColor-emphasis` | `#25292e` | `#2c272b` | `#3d444d` | `#494048` |
| `--bgColor-inset` | `var(--bgColor-muted)` | not tinted | `#010409` | `#060206` |
| `--bgColor-muted` | `#f6f8fa` | `#faf6fa` | `#151b23` | `#20181f` |
| `--bgColor-neutral-emphasis` | `#59636e` | `#695e68` | `#656c76` | `#726870` |
| `--bgColor-neutral-muted` | `#818b981f` | `#9285911f` | `#656c7633` | `#72687033` |
| `--borderColor-default` | `#d1d9e0` | `#ddd5dc` | `#3d444d` | `#494048` |
| `--borderColor-disabled` | `#818b981a` | `#9285911a` | `#656c761a` | `#7268701a` |
| `--borderColor-emphasis` | `#818b98` | `#928591` | `#656c76` | `#726870` |
| `--borderColor-muted` | `#d1d9e0b3` | `#ddd5dcb3` | `#3d444db3` | `#494048b3` |
| `--borderColor-neutral-emphasis` | `#59636e` | `#695e68` | `var(--borderColor-emphasis)` | not tinted |
| `--borderColor-translucent` | `#1f232826` | `#26212526` | `#ffffff26` | `#fffdff26` |
| `--button-inactive-bgColor` | `#e6eaef` | `#ede8ec` | `#262c36` | `#322831` |
| `--button-inactive-fgColor` | `#59636e` | `#695e68` | `#9198a1` | `#9d949c` |
| `--control-bgColor-active` | `#e6eaef` | `#ede8ec` | `#2a313c` | `#372d36` |
| `--control-bgColor-hover` | `#eff2f5` | `#f4f0f4` | `#262c36` | `#322831` |
| `--control-bgColor-rest` | `#f6f8fa` | `#faf6fa` | `#212830` | `#2d242c` |
| `--control-borderColor-selected` | `var(--control-bgColor-selected)` | not tinted | `#f0f6fc` | `#f9f3f9` |
| `--control-fgColor-rest` | `#25292e` | `#2c272b` | `var(--fgColor-default)` | not tinted |
| `--control-transparent-bgColor-active` | `#818b9826` | `#92859126` | `#656c7640` | `#72687040` |
| `--control-transparent-bgColor-hover` | `#818b981a` | `#9285911a` | `#656c7633` | `#72687033` |
| `--control-transparent-bgColor-selected` | `#818b9826` | `#92859126` | `#656c7633` | `#72687033` |
| `--controlKnob-bgColor-rest` | `#ffffff` | `#fffdff` | `#262c36` | `#322831` |
| `--controlTrack-bgColor-active` | `#dae0e7` | `#e4dde3` | `#2f3742` | `#3d323c` |
| `--controlTrack-bgColor-hover` | `#e0e6eb` | `#e9e3e8` | `#2a313c` | `#372d36` |
| `--controlTrack-bgColor-rest` | `#e6eaef` | `#ede8ec` | `#010409` | `#060206` |
| `--controlTrack-fgColor-rest` | `#59636e` | `#695e68` | `var(--fgColor-muted)` | not tinted |
| `--fgColor-default` | `#1f2328` | `#262125` | `#f0f6fc` | `#f9f3f9` |
| `--fgColor-disabled` | `#818b98` | `#928591` | `#656c76` | `#726870` |
| `--fgColor-muted` | `#59636e` | `#695e68` | `#9198a1` | `#9d949c` |
| `--fgColor-neutral` | `#59636e` | `#695e68` | `#9198a1` | `#9d949c` |
| `--overlay-backdrop-bgColor` | `#c8d1da66` | `#d6ccd566` | `#21283066` | `#2d242c66` |
| `--overlay-bgColor` | `#ffffff` | `#fffdff` | `#010409` | `#060206` |
| `--overlay-borderColor` | `#d1d9e080` | `#ddd5dc80` | `var(--borderColor-muted)` | not tinted |
| `--skeletonLoader-bgColor` | `#818b981a` | `#9285911a` | `#656c7633` | `#72687033` |
| `--timelineBadge-bgColor` | `var(--bgColor-muted)` | not tinted | `#212830` | `#2d242c` |

#### Contrast of the Full Theme

The table lists every pair in `colorContrast.config.ts` that involves a token `theme.css` overrides, directly or through a `var()` reference. It adds seven Markspace pairs, which are primary button text on hover and active, default text on the search highlight and on the selection, and the focus outline on the default, muted and control backgrounds. A translucent background is composited over `--bgColor-default`, and a translucent foreground over its background. Ratios are rounded down to two decimals. The Primer columns give the ratio with Primer's own blue and green values.

| Foreground | Background | Required | Primer light | Markspace light | Primer dark | Markspace dark |
| --- | --- | --- | --- | --- | --- | --- |
| `--fgColor-default` | `--bgColor-default` | 4.5 | 15.79 | 15.63 | 17.38 | 17.34 |
| `--fgColor-muted` | `--bgColor-default` | 4.5 | 6.11 | 6.09 | 6.49 | 6.45 |
| `--fgColor-default` | `--bgColor-muted` | 4.5 | 14.83 | 14.79 | 15.90 | 15.85 |
| `--fgColor-muted` | `--bgColor-muted` | 4.5 | 5.74 | 5.77 | 5.94 | 5.89 |
| `--fgColor-default` | `--bgColor-inset` | 4.5 | 14.83 | 14.79 | 18.86 | 18.85 |
| `--fgColor-muted` | `--bgColor-inset` | 4.5 | 5.74 | 5.77 | 7.05 | 7.01 |
| `--control-fgColor-rest` | `--bgColor-default` | 4.5 | 14.63 | 14.48 | 17.38 | 17.34 |
| `--control-fgColor-rest` | `--bgColor-muted` | 4.5 | 13.74 | 13.70 | 15.90 | 15.85 |
| `--control-fgColor-rest` | `--bgColor-inset` | 4.5 | 13.74 | 13.70 | 18.86 | 18.85 |
| `--control-fgColor-rest` | `--control-bgColor-rest` | 4.5 | 13.74 | 13.70 | 13.67 | 13.72 |
| `--control-fgColor-placeholder` | `--bgColor-default` | 4.5 | 6.11 | 6.09 | 6.49 | 6.45 |
| `--control-fgColor-rest` | `--control-bgColor-active` | 4.5 | 12.10 | 12.11 | 12.03 | 12.07 |
| `--control-fgColor-rest` | `--control-bgColor-hover` | 4.5 | 13.02 | 12.99 | 12.89 | 12.95 |
| `--control-fgColor-rest` | `--control-bgColor-selected` | 4.5 | 13.74 | 13.70 | 13.67 | 13.72 |
| `--button-invisible-fgColor-rest` | `--bgColor-muted` | 4.5 | 13.74 | 13.70 | 15.90 | 15.85 |
| `--button-invisible-fgColor-hover` | `--button-invisible-bgColor-hover` | 4.5 | 13.19 | 13.05 | 14.47 | 14.45 |
| `--button-invisible-iconColor-rest` | `--bgColor-muted` | 4.5 | 5.74 | 5.77 | 5.94 | 5.89 |
| `--button-invisible-iconColor-hover` | `--button-invisible-bgColor-hover` | 4.5 | 5.51 | 5.49 | 5.40 | 5.37 |
| `--button-primary-fgColor-rest` | `--button-primary-bgColor-rest` | 4.5 | 4.51 | 4.51 | 4.63 | 6.53 |
| `--fgColor-default` | `--bgColor-neutral-muted` | 4.5 | 13.95 | 13.81 | 14.47 | 14.45 |
| `--fgColor-default` | `--bgColor-accent-muted` | 4.5 | 13.88 | 14.27 | 15.49 | 15.49 |
| `--fgColor-default` | `--bgColor-success-muted` | 4.5 | 14.19 | 14.22 | 14.55 | 14.62 |
| `--fgColor-default` | `--bgColor-danger-muted` | 4.5 | 13.77 | 13.80 | 15.78 | 15.61 |
| `--fgColor-default` | `--bgColor-attention-muted` | 4.5 | 14.65 | 14.29 | 14.61 | 14.53 |
| `--fgColor-default` | `--bgColor-done-muted` | 4.5 | 14.21 | 14.24 | 14.25 | 14.19 |
| `--fgColor-default` | `--bgColor-sponsors-muted` | 4.5 | 14.24 | 14.27 | 15.61 | 15.49 |
| `--fgColor-neutral` | `--bgColor-default` | 4.5 | 6.11 | 6.09 | 6.49 | 6.45 |
| `--fgColor-neutral` | `--bgColor-muted` | 4.5 | 5.74 | 5.77 | 5.94 | 5.89 |
| `--fgColor-neutral` | `--bgColor-inset` | 4.5 | 5.74 | 5.77 | 7.05 | 7.01 |
| `--fgColor-accent` | `--bgColor-default` | 4.5 | 5.19 | 4.98 | 6.10 | 7.53 |
| `--fgColor-accent` | `--bgColor-muted` | 4.5 | 4.87 | 4.71 | 5.58 | 6.89 |
| `--fgColor-accent` | `--bgColor-inset` | 4.5 | 4.87 | 4.71 | 6.62 | 8.19 |
| `--fgColor-success` | `--bgColor-default` | 4.5 | 5.07 | 5.01 | 7.44 | 7.45 |
| `--fgColor-success` | `--bgColor-muted` | 4.5 | 4.77 | 4.74 | 6.81 | 6.82 |
| `--fgColor-success` | `--bgColor-inset` | 4.5 | 4.77 | 4.74 | 8.08 | 8.11 |
| `--fgColor-danger` | `--bgColor-default` | 4.5 | 5.24 | 5.18 | 5.64 | 5.65 |
| `--fgColor-danger` | `--bgColor-muted` | 4.5 | 4.92 | 4.90 | 5.16 | 5.16 |
| `--fgColor-danger` | `--bgColor-inset` | 4.5 | 4.92 | 4.90 | 6.12 | 6.14 |
| `--fgColor-attention` | `--bgColor-default` | 4.5 | 4.86 | 4.97 | 7.49 | 7.48 |
| `--fgColor-attention` | `--bgColor-muted` | 4.5 | 4.57 | 4.70 | 6.85 | 6.84 |
| `--fgColor-attention` | `--bgColor-inset` | 4.5 | 4.57 | 4.70 | 8.13 | 8.14 |
| `--fgColor-severe` | `--bgColor-default` | 4.5 | 5.03 | 4.97 | 5.61 | 5.62 |
| `--fgColor-severe` | `--bgColor-muted` | 4.5 | 4.72 | 4.70 | 5.13 | 5.14 |
| `--fgColor-severe` | `--bgColor-inset` | 4.5 | 4.72 | 4.70 | 6.09 | 6.11 |
| `--fgColor-done` | `--bgColor-default` | 4.5 | 5.04 | 4.98 | 6.32 | 6.33 |
| `--fgColor-done` | `--bgColor-muted` | 4.5 | 4.73 | 4.71 | 5.78 | 5.79 |
| `--fgColor-done` | `--bgColor-inset` | 4.5 | 4.73 | 4.71 | 6.86 | 6.88 |
| `--fgColor-upsell` | `--bgColor-default` | 4.5 | 5.04 | 4.98 | 6.32 | 6.33 |
| `--fgColor-upsell` | `--bgColor-muted` | 4.5 | 4.73 | 4.71 | 5.78 | 5.79 |
| `--fgColor-upsell` | `--bgColor-inset` | 4.5 | 4.73 | 4.71 | 6.86 | 6.88 |
| `--fgColor-sponsors` | `--bgColor-default` | 4.5 | 5.04 | 4.98 | 5.62 | 7.53 |
| `--fgColor-sponsors` | `--bgColor-muted` | 4.5 | 4.73 | 4.71 | 5.14 | 6.89 |
| `--fgColor-sponsors` | `--bgColor-inset` | 4.5 | 4.73 | 4.71 | 6.10 | 8.19 |
| `--fgColor-neutral` | `--bgColor-neutral-muted` | 4.5 | 5.40 | 5.38 | 5.40 | 5.37 |
| `--fgColor-accent` | `--bgColor-accent-muted` | 4.5 | 4.56 | 4.55 | 5.44 | 6.73 |
| `--fgColor-attention` | `--bgColor-attention-muted` | 4.5 | 4.51 | 4.54 | 6.30 | 6.27 |
| `--fgColor-sponsors` | `--bgColor-sponsors-muted` | 4.5 | 4.55 | 4.55 | 5.05 | 6.73 |
| `--fgColor-onEmphasis` | `--bgColor-neutral-emphasis` | 4.5 | 6.11 | 6.17 | 5.30 | 5.34 |
| `--fgColor-onEmphasis` | `--bgColor-accent-emphasis` | 4.5 | 5.19 | 5.04 | 4.63 | 4.57 |
| `--fgColor-onEmphasis` | `--bgColor-attention-emphasis` | 4.5 | 4.86 | 5.03 | 4.64 | 4.65 |
| `--fgColor-onEmphasis` | `--bgColor-sponsors-emphasis` | 4.5 | 5.04 | 5.04 | 4.57 | 4.57 |
| `--control-borderColor-emphasis` | `--bgColor-default` | 3 | 3.45 | 3.46 | 3.56 | 3.54 |
| `--control-borderColor-emphasis` | `--bgColor-muted` | 3 | 3.24 | 3.27 | 3.26 | 3.24 |
| `--button-primary-fgColor-rest` | `--button-primary-bgColor-hover` | 4.5 | 4.94 | 4.92 | 4.08 | 5.67 |
| `--button-primary-fgColor-rest` | `--button-primary-bgColor-active` | 4.5 | 5.48 | 5.45 | 3.61 | 4.94 |
| `--fgColor-default` | `--highlight-neutral-bgColor` | 4.5 | 14.65 | 11.86 | 8.10 | 8.06 |
| `--fgColor-default` | `--selection-bgColor` | 4.5 | 11.84 | 11.71 | 6.71 | 6.62 |
| `--focus-outlineColor` | `--bgColor-default` | 3 | 5.19 | 4.98 | 4.08 | 4.14 |
| `--focus-outlineColor` | `--bgColor-muted` | 3 | 4.87 | 4.71 | 3.73 | 3.78 |
| `--focus-outlineColor` | `--control-bgColor-rest` | 3 | 4.87 | 4.71 | 3.21 | 3.27 |

Every pair passes. The lowest text ratio is 4.51, for `--button-primary-fgColor-rest` on `--button-primary-bgColor-rest` in light. The lowest border and focus ratio is 3.24, for `--control-borderColor-emphasis` on `--bgColor-muted` in dark.

### 2.5 Mapping of Markspace Tokens

The table maps each custom property of the earlier Markspace stylesheets to a Primer token. The light and dark columns give Primer's value, with the accent override applied. Where Primer has two tokens for one Markspace token, the row says which use takes which.

| Markspace token | Primer token | Light | Dark |
| --- | --- | --- | --- |
| `--bg` | `--bgColor-default` | `#fffdff` | `#140f14` |
| `--surface` (page, cards, panels) | `--bgColor-default` | `#fffdff` | `#140f14` |
| `--surface` (menus, popovers, dialogs) | `--overlay-bgColor` | `#fffdff` | `#060206` |
| `--surface-2` | `--bgColor-muted` | `#faf6fa` | `#20181f` |
| `--surface-3` | `--bgColor-neutral-muted` | `#9285911f` | `#72687033` |
| `--sidebar-bg` | `--bgColor-default`. The pane is separated by a divider, not a fill | `#fffdff` | `#140f14` |
| `--border` | `--borderColor-muted` | `#ddd5dcb3` | `#494048b3` |
| `--border-strong` | `--borderColor-default` | `#ddd5dc` | `#494048` |
| `--border-strong` (control borders) | `--control-borderColor-rest` | `#ddd5dc` | `#494048` |
| `--hover` | `--control-transparent-bgColor-hover` | `#9285911a` | `#72687033` |
| `--active` (pressed) | `--control-transparent-bgColor-active` | `#92859126` | `#72687040` |
| `--active` (current nav item) | `--control-transparent-bgColor-selected` | `#92859126` | `#72687033` |
| `--text` | `--fgColor-default` | `#262125` | `#f9f3f9` |
| `--text-muted` | `--fgColor-muted` | `#695e68` | `#9d949c` |
| `--text-subtle` | `--fgColor-muted`. Primer has no third text level, and `--fgColor-disabled` is for disabled text only | `#695e68` | `#9d949c` |
| `--accent` (text, links, icons) | `--fgColor-accent` | `#bf3989` | `#f778ba` |
| `--accent` (fills, selected indicators) | `--bgColor-accent-emphasis` | `#bf3989` | `#bf4b8a` |
| `--accent` (primary button) | `--button-primary-bgColor-rest`. Pink, not green (2.4) | `#c94192` | `#9e3670` |
| `--accent-hover` (primary button) | `--button-primary-bgColor-hover` | `#c13b8b` | `#ab3e7a` |
| `--accent-hover` (links) | none. A hovered link keeps `--fgColor-accent` and gains an underline | | |
| `--accent-contrast` | `--fgColor-onEmphasis` | `#ffffff` | `#ffffff` |
| `--accent-soft` | `--bgColor-accent-muted` | `#ffeff7` | `#db61a21a` |
| `--danger` (text) | `--fgColor-danger` | `#d1242f` | `#f85149` |
| `--danger` (fills) | `--bgColor-danger-emphasis` | `#cf222e` | `#da3633` |
| `--danger-soft` | `--bgColor-danger-muted` | `#ffebe9` | `#f851491a` |
| `--ok` | `--fgColor-success` | `#1a7f37` | `#3fb950` |
| `--ok-soft` | `--bgColor-success-muted` | `#dafbe1` | `#2ea04326` |
| `--warn` | `--fgColor-attention` | `#bc4c00` | `#f0883e` |
| `--warn-soft` | `--bgColor-attention-muted` | `#fff1e5` | `#db6d2826` |
| `--code-bg` (inline code) | `--bgColor-neutral-muted` | `#9285911f` | `#72687033` |
| `--code-bg` (code blocks) | `--bgColor-muted` | `#faf6fa` | `#20181f` |
| `--focus-ring` | `--focus-outlineColor` | `#bf3989` | `#bf4b8a` |
| `--mark` (search hits in text) | `--highlight-neutral-bgColor` | `#ffd8b5` | `#f0883e66` |
| `--highlight` (comment anchors) | `--bgColor-accent-muted` | `#ffeff7` | `#db61a21a` |
| `--highlight-strong` (active anchor) | `--borderColor-accent-muted` | `#ff80c866` | `#db61a266` |
| `--highlight-edge` | `--borderColor-accent-emphasis` | `#bf3989` | `#bf4b8a` |
| `--highlight` (tracked insertion) | `--diffBlob-additionWord-bgColor` | `#aceebb` | `#2ea04366` |
| `--danger-soft` (tracked deletion) | `--diffBlob-deletionWord-bgColor` | `#ffcecb` | `#f8514966` |
| `--scrim` | `--overlay-backdrop-bgColor` | `#d6ccd566` | `#2d242c66` |
| `--shadow-sm` | `--shadow-resting-small` | `0 1px 1px 0 #1f23280a, 0 1px 2px 0 #1f232808` | `0 1px 1px 0 #01040999, 0 1px 3px 0 #01040999` |
| `--shadow` (menus, popovers) | `--shadow-floating-small` | `0 0 0 1px #d1d9e040, 0 6px 12px -3px #25292e0a, 0 6px 18px 0 #25292e1f` | `0 0 0 1px #3d444d, 0 6px 12px -3px #01040966, 0 6px 18px 0 #01040966` |
| `--shadow-lg` (dialogs, sheets) | `--shadow-floating-small`. Primer React's Dialog and Overlay CSS use the small floating shadow over the backdrop | `0 0 0 1px #d1d9e040, 0 6px 12px -3px #25292e0a, 0 6px 18px 0 #25292e1f` | `0 0 0 1px #3d444d, 0 6px 12px -3px #01040966, 0 6px 18px 0 #01040966` |
| `--radius-sm` | `--borderRadius-medium` | 6px | 6px |
| `--radius` | `--borderRadius-medium` | 6px | 6px |
| `--radius-lg` (dialogs, large overlays) | `--borderRadius-large` | 12px | 12px |
| `--font-sans` | `--fontStack-system` | section 3.1 | |
| `--font-mono` | `--fontStack-monospace` | section 3.1 | |
| `--space-1` | `--base-size-4` | 4px | |
| `--space-2` | `--base-size-8` | 8px | |
| `--space-3` | `--base-size-12` | 12px | |
| `--space-4` | `--base-size-16` | 16px | |
| `--space-5` | `--base-size-24` | 24px | |
| `--space-6` | `--base-size-32` | 32px | |
| `--space-7` | `--base-size-48` | 48px | |
| `--pad-panel` (20px) | `--stack-padding-normal` | 16px | |
| `--gap-section` (32px) | `--base-size-32` | 32px | |
| `--gap-field` (14px) | `--stack-gap-normal` | 16px | |
| `--speed` (hover) | `--motion-transition-hover` | 100ms ease | |
| `--speed`, `--speed-slow`, `--ms-ease` (expand, collapse, state change) | `--motion-transition-stateChange` | 200ms `cubic-bezier(0.6, 0, 0.2, 1)` | |
| `--sidebar-w` | SplitPageLayout pane, width `medium` (section 5.1) | 256px from 768px, 296px from 1012px | |
| `--rail-w` | PageLayout pane at `end`, width `large` (section 5.1) | 256px from 768px, 320px from 1012px | |
| `--topbar-h` | App header height (section 5.1). Primer documents none, so it is a 32px control row with 16px padding above and below | 64px | |
| `--measure`, `--ms-measure` | none. Stays a Markspace token for the note text width | 72ch | |
| `--ms-body-size` | `--text-body-size-large` | 16px | |
| `--ms-line` | `--text-body-lineHeight-large` | 1.5 | |
| `--ms-bar-h` | `--control-medium-size` plus 2 × `--base-size-8` | 48px | |
| `--safe-top`, `--safe-right`, `--safe-bottom`, `--safe-left` | none. Stay Markspace tokens for `env(safe-area-inset-*)` | | |
| `--ms-kbar-space`, `--status-bottom`, `--comment-button-dim`, `--editor-toolbar-vert-offset` | none. Stay Markspace layout variables set by script | | |
| `--total-scale-factor`, `--scale-round-x`, `--scale-round-y` | none. Set by pdf.js | | |
| `--avatar-bg` | `--avatar-bgColor` for image avatars. Initials avatars keep the stored user colour, section 5.8 | `#ffffff` | `#ffffff1a` |
| `.uc-s1` (usage chart series 1, `usage.css`) | `--data-pink-color-emphasis` | `#ce2c85` | `#d34591` |
| `.uc-s2` | `--data-orange-color-emphasis` | `#eb670f` | `#984b10` |
| `.uc-s3` | `--data-blue-color-emphasis` | `#006edb` | `#0576ff` |
| `.uc-s4` | `--data-green-color-emphasis` | `#30a147` | `#2f6f37` |
| `.uc-s5` | `--data-purple-color-emphasis` | `#894ceb` | `#975bf1` |
| `.uc-s6` | `--data-teal-color-emphasis` | `#179b9b` | `#106c70` |
| `.uc-s7` | `--data-plum-color-emphasis` | `#a830e8` | `#b643ef` |
| `.uc-other` | `--data-gray-color-emphasis` | `#808fa3` | `#576270` |
| `--comment-fg-color` | `--fgColor-default` | `#262125` | `#f9f3f9` |

The editor syntax classes in `static/css/editor.css` use Primer's `prettylights` syntax tokens, the tokens GitHub uses for code highlighting.

| Class | Primer token | Light | Dark |
| --- | --- | --- | --- |
| `.cm-ms-t-keyword` | `--color-prettylights-syntax-keyword` | `#cf222e` | `#ff7b72` |
| `.cm-ms-t-string` | `--color-prettylights-syntax-string` | `#0a3069` | `#a5d6ff` |
| `.cm-ms-t-comment` (italic) | `--color-prettylights-syntax-comment` | `#59636e` | `#9198a1` |
| `.cm-ms-t-number`, `.cm-ms-t-property` | `--color-prettylights-syntax-constant` | `#0550ae` | `#79c0ff` |
| `.cm-ms-t-function`, `.cm-ms-t-type` | `--color-prettylights-syntax-entity` | `#6639ba` | `#d2a8ff` |
| `.cm-ms-t-tag` | `--color-prettylights-syntax-entity-tag` | `#0550ae` | `#7ee787` |
| `.cm-activeLine` | `--bgColor-muted` | `#faf6fa` | `#20181f` |
| `.cm-selectionBackground` | `--selection-bgColor` | `#bf398933` | `#bf4b8ab3` |
| `.cm-cursor` | `--codeMirror-cursor-fgColor`, which is `var(--fgColor-default)` | `#262125` | `#f9f3f9` |

`static/css/base.css` starts with a temporary alias block, such as `--text: var(--fgColor-default)`, for the old token names that stylesheets still use. The block goes once no rule uses these names. Outside `theme.css`, Markspace CSS takes its colours from tokens. The exceptions are the rows above marked "none", the black stops of mask gradients, the white PDF page and the black video background.

## 3. Typography, Spacing, Radius, Shadow, Motion and Breakpoints

### 3.1 Font Stacks

| Use | Token | Value in 11.10.0 |
| --- | --- | --- |
| All UI and note text | `--fontStack-system` | `"Mona Sans VF", -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans Backtick Fix", "Noto Sans", Helvetica, Arial, sans-serif, "Apple Color Emoji", "Segoe UI Emoji"` |
| Code, `kbd`, source mode, share URLs, token values | `--fontStack-monospace` | `ui-monospace, SFMono-Regular, SF Mono, Menlo, Consolas, Liberation Mono, monospace` |

Markspace does not vendor Mona Sans. With no `@font-face` for `"Mona Sans VF"`, browsers skip that name and use the system font. `--fontStack-sansSerif` and `--fontStack-sansSerifDisplay` hold the same value in 11.10.0. `-webkit-font-smoothing: antialiased` is removed, because Primer does not set it.

### 3.2 Type Scale

| Role | Token shorthand | Size / line height / weight | Markspace use |
| --- | --- | --- | --- |
| Body, medium | `--text-body-shorthand-medium` | 14px / 1.5 (21px) / 400 | Default UI text. `body` font |
| Body, large | `--text-body-shorthand-large` | 16px / 1.5 / 400 | Note text in the editor and reading view, rendered markdown, comments body in the reading view |
| Body, small | `--text-body-shorthand-small` | 12px / 1.625 / 400 | Captions under fields, row metadata, timestamps |
| Caption | `--text-caption-shorthand` | 12px / 1.25 / 400 | Single-line compact text only, such as counters and keyboard hints |
| Title, small | `--text-title-shorthand-small` | 16px / 1.5 / 600 | Section and list headings, panel titles, form block titles, dialog titles |
| Title, medium | `--text-title-shorthand-medium` | 20px / 1.625 / 600 | Page titles of app pages (Settings, Administration, Trash, Spaces) on every viewport. Note titles on narrow viewports |
| Title, large | `--text-title-shorthand-large` | 32px / 1.5 / 600 | Not used directly. Note titles use PageHeader's large variant, which is 32px / 1.5 at weight 400 in Primer React's CSS, on regular and wide viewports, because a note is user-created content |
| Subtitle | `--text-subtitle-shorthand` | 20px / 1.625 / 400 | Secondary text beside a page title, where one exists |
| Display | `--text-display-shorthand` | 40px / 1.375 / 500 | Not used |
| Code block | `--text-codeBlock-shorthand` | 13px / 1.5 / 400, mono | Code blocks, source mode, CSV and text viewers |
| Inline code | `--text-codeInline-shorthand` | 0.9285em, mono | Inline code in notes and UI |

Rules:

- Weights are 400, 500 and 600 only. Markspace's 520, 540, 560, 580, 620, 650, 680, 700 and 750 map to the nearest of these, with headings at 600.
- Letter spacing is 0 everywhere. The negative tracking on headings and the positive tracking on labels are removed.
- Uppercase labels (`.sidenav-title`, `.section-title`, `.menu-label`, `.table th`, `.ms-section-title`, `.switcher-group`, `.action-label`, `.new-space-label`, `.ai-scope`) become sentence case at `--text-body-shorthand-small` weight 600 in `--fgColor-muted`. That is the style of an ActionList group heading, which is 12px at weight 600 with an 18px line height in `--fgColor-muted`. Primer's content guidance asks for sentence case, and its Button guidance forbids all caps.
- Labels, buttons, headings and menu items have no final punctuation. Form labels have no colon.
- Text inputs use 14px, and 16px on iOS below 768px (5.3).
- Markdown headings in notes use the values in `dist/markdown.css` of `@primer/css` 22.3.1. h1 is 2em, h2 1.5em, h3 1.25em, h4 1em, h5 0.875em and h6 0.85em. All are weight 600 with line height 1.25, 24px above and 16px below. h1 and h2 have a 1px `--borderColor-muted` bottom border with 0.3em padding above it.

### 3.3 Spacing and Sizing

Primer sizes are multiples of 4px, with 2px and 6px for fine adjustments.

| Token group | Values |
| --- | --- |
| `--base-size-*` | 2, 4, 6, 8, 12, 16, 20, 24, 28, 32, 36, 40, 44, 48, 64, 80, 96, 112, 128 px |
| `--space-*` | xxs 2, xs 4, sm 8, md 12, lg 16, xl 24 px |
| `--stack-gap-*` | condensed 8, normal 16, spacious 24 px |
| `--stack-padding-*` | condensed 8, normal 16, spacious 24 px |
| `--control-{size}-size` | xsmall 24, small 28, medium 32, large 40, xlarge 48 px |
| `--control-{size}-paddingBlock` | xsmall 2, small 4, medium 6, large 10, xlarge 14 px |
| `--control-{size}-paddingInline-*` | xsmall 4 / 8 / 12, other sizes 8 / 12 / 16 px (condensed / normal / spacious) |
| `--control-{size}-gap` | xsmall 4, small 4, medium 8, large 8, xlarge 8 px |
| `--controlStack-{size}-gap-auto` | 8px on fine pointers, 12px (medium, large) or 16px (small) on coarse pointers |
| `--control-minTarget-*` | fine 16px, coarse 44px, `auto` switches by `(pointer)` |
| `--overlay-width-*` | xsmall 192, small 320, medium 480, large 640, xlarge 960 px |
| `--overlay-height-*` | small 256, medium 320, large 432, xlarge 600 px |
| `--overlay-padding-*` | condensed 8, normal 16 px. `--overlay-paddingBlock-*` condensed 4, normal 12 px |
| `--overlay-offset` | 4px between an anchor and its overlay |
| `--spinner-size-*` | small 16, medium 32, large 64 px, stroke `--spinner-strokeWidth-default` 2px |

How Markspace uses them:

| Place | Value |
| --- | --- |
| Gap between stacked form fields | `--stack-gap-normal` 16px |
| Gap between a label, its input and its caption | `--base-size-8` between label and input, `--base-size-4` above the caption |
| Gap between sections of a page | `--base-size-32` |
| Padding inside a bordered box, form block or card | `--stack-padding-normal` 16px |
| Content and pane padding | 16px below 1012px, 24px from 1012px. This is PageLayout's `normal` spacing in Primer React's CSS. Primer's layout page gives 24px from 1280px for content, and this document follows the CSS. The padding sits on the content or pane region, not on its parent |
| Full page maximum width | 1280px including padding. Sign-in, sign-up, setup and password pages 320px |
| Gap between buttons in a row | `--base-size-8` |
| Gap between an icon and a label inside a control | `--control-{size}-gap` |

### 3.4 Radius and Border Width

| Token | Value | Markspace use |
| --- | --- | --- |
| `--borderRadius-small` | 3px | Counter-free small shapes such as `kbd` |
| `--borderRadius-medium` (`--borderRadius-default`) | 6px | Buttons, inputs, menus, popovers, cards, boxes, banners, rows with a hover fill |
| `--borderRadius-large` | 12px | Dialogs, bottom sheets (top corners), ActionMenu, SelectPanel and Autocomplete overlays |
| `--borderRadius-full` | 624.9375rem | Counters, labels, tokens, avatars, toggle switch |
| `--overlay-borderRadius` | 6px | Popover only. Primer React's Overlay and Dialog CSS use `--borderRadius-large` instead of this token |
| `--borderWidth-thin` (`--borderWidth-default`) | 1px | All borders and dividers |
| `--borderWidth-thick` | 2px | Focus outline, underline of the selected UnderlineNav tab |
| `--borderWidth-thicker` | 4px | Not used |
| `--focus-outline-width` | 2px | Focus outline |
| `--focus-outline-offset` | -2px | Focus outline inset on controls |

Markspace's 10px, 14px, 16px and 18px radii are removed.

### 3.5 Shadow and Elevation

| Token | Use in Markspace |
| --- | --- |
| `--shadow-resting-xsmall` | Default buttons (`--button-default-shadow-resting` is the equivalent) |
| `--shadow-resting-small` | Cards that lift on hover, selected SegmentedControl item |
| `--shadow-resting-medium` | Not used |
| `--shadow-floating-small` | ActionMenu, SelectPanel, Popover, Autocomplete list, Dialog, ConfirmationDialog, bottom sheet. Tooltip has no shadow |
| `--shadow-floating-medium` | Not used |
| `--shadow-floating-large` | Not used |
| `--shadow-floating-xlarge` | Not used |
| `--shadow-inset` | Text inputs, textareas, selects |

Layers use Primer's z-index tokens.

| Token | Value | Markspace layer |
| --- | --- | --- |
| `--zIndex-sticky` | 100 | App header, sticky panel headers, rail tab strip, phone keyboard bar |
| `--zIndex-dropdown` | 200 | ActionMenu, SelectPanel, Autocomplete, slash menu |
| `--zIndex-overlay` | 300 | Backdrop, sidebar drawer, rail sheet |
| `--zIndex-modal` | 400 | Dialog |
| `--zIndex-popover` | 500 | Tooltip, Popover |
| `--zIndex-skipLink` | 600 | Skip link |

### 3.6 Motion

| Token | Value | Markspace use |
| --- | --- | --- |
| `--motion-transition-hover` | 100ms `cubic-bezier(0.25, 0.1, 0.25, 1)` | Background and colour changes on hover and press |
| `--motion-transition-stateChange` | 200ms `cubic-bezier(0.6, 0, 0.2, 1)` | Expand and collapse, toggle switch knob, tree chevron rotation |
| `--motion-transition-enter` | 300ms `cubic-bezier(0.3, 0.8, 0.6, 1)` | Dialog, bottom sheet, drawer and overlay entering |
| `--motion-transition-exit` | 200ms `cubic-bezier(0.7, 0.1, 0.75, 0.9)` | Dialog, bottom sheet, drawer and overlay leaving |
| `--motion-easing-linear` | `cubic-bezier(0, 0, 1, 1)` | Spinner rotation, indeterminate progress |
| `--motion-duration-long` | 500ms | Not used |

Primer React's component CSS uses its own values for some components, and Markspace copies them there.

| Component | Motion |
| --- | --- |
| Button | Colour, fill, background and border change in 80ms `cubic-bezier(0.65, 0, 0.35, 1)`. No transition while pressed |
| ActionList item | Background changes in 33.333ms linear |
| Dialog, centred | Fades and scales from 0.5 to 1 in 200ms `cubic-bezier(0.33, 1, 0.68, 1)`. The backdrop fades in over 200ms |
| Dialog, bottom sheet or side sheet | Slides in over 250ms `cubic-bezier(0.33, 1, 0.68, 1)` |
| ActionMenu, SelectPanel, anchored overlays | Fade in over 200ms `cubic-bezier(0.33, 1, 0.68, 1)` |
| Tooltip | Fades in over 100ms ease-in |
| SegmentedControl knob | Slides in 200ms `--motion-easing-move` |

Markspace writes every transition and animation inside `@media (prefers-reduced-motion: no-preference)`, as Primer's motion guidance and component CSS do. The Spinner is the one exception. Primer's loading guidance says loading animation stays on for users who prefer reduced motion and must stay subtle. Primer also requires that minor animations stop within 5 seconds and that nothing flashes more than 3 times a second.

### 3.7 Breakpoints and Viewport Ranges

| Token | Value |
| --- | --- |
| `--breakpoint-xsmall` | 20rem, 320px |
| `--breakpoint-small` | 34rem, 544px |
| `--breakpoint-medium` | 48rem, 768px |
| `--breakpoint-large` | 63.25rem, 1012px |
| `--breakpoint-xlarge` | 80rem, 1280px |
| `--breakpoint-xxlarge` | 87.5rem, 1400px |

CSS custom properties cannot appear in media queries, so Markspace writes Primer's viewport ranges out in full.

| Range | Media query | Markspace use |
| --- | --- | --- |
| narrow | `(max-width: calc(48rem - 0.02px))` | Phones, 390px included |
| regular | `(min-width: 48rem)` | Tablets and desktops |
| wide | `(min-width: 87.5rem)` | Large desktops |
| narrowLandscape | `(max-width: calc(63.25rem - 0.02px)) and (max-height: calc(34rem - 0.02px)) and (orientation: landscape)` | Phones turned sideways |
| large (PageLayout step) | `(min-width: 63.25rem)` | Pane widths grow to 296px and 320px and layout padding to 24px |

Markspace's current breakpoints (600px, 699px, 700px, 900px, 1100px) are replaced.

| Current query | Becomes |
| --- | --- |
| `max-width: 600px`, `max-width: 699px`, `max-width: 700px` | narrow |
| `max-width: 900px` (sidebar becomes a drawer) | narrow. The sidebar becomes a left Dialog side sheet (section 5.1) |
| `max-width: 1100px` (rail becomes a sheet) | narrow for the full-screen rail Dialog. Between 768px and 1399px the sidebar and the rail share Primer's two columns (section 5.1) |
| `pointer: coarse` for 44px targets | unchanged, through `--control-minTarget-auto` |

## 4. Icons

### 4.1 Package

Octicons are GitHub's icon set. `@primer/octicons` 19.36.0 is MIT licensed, copyright GitHub Inc. 2026. `build/svg` holds 750 SVG files for 388 icons, at heights 12, 16 and 24, and some at 48 and 96. All 16px files together are 222 394 bytes, and all 24px files 213 058 bytes. A single icon is 175 to 3203 bytes. `build/data.json` (997 633 bytes) holds the path data of every icon at every height. `build/build.css` (109 bytes) holds the one rule below.

```css
.octicon { display: inline-block; vertical-align: text-top; fill: currentColor; overflow: visible; }
```

Octicons are filled shapes on a 16 × 16 or 24 × 24 grid. The current Markspace icons are 1.6px strokes on a 20 × 20 grid, so no current path survives.

### 4.2 Delivery Without a Build Step

| Piece | File | Content |
| --- | --- | --- |
| SVG files | `internal/web/static/vendor/octicons-19.36.0/{name}-{16,24}.svg` | Only the icons in the table below, copied unchanged, plus `LICENSE` |
| Server templates | Go template function `octicon` in `internal/web/octicon.go` | `{{ octicon "kebab-horizontal" 16 }}` reads the embedded file and returns the `svg` element with `class="octicon"`, `aria-hidden="true"` and `focusable="false"` |
| Client | `internal/web/static/js/octicons.js` | `export const PATHS = {"x-16": "<path d=…/>", …}`, copied from `data.json` `heights[16].path` and `heights[24].path` for the same icons |
| Client helper | `icon(name, size = 16)` in `ui.js` | Builds `<svg class="octicon" viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true" focusable="false">` from `PATHS`. Accepts only 12, 16 and 24 |

`icon()` keeps its name so callers do not change. The internal table maps each old Markspace name to its Octicon, and new code passes Octicon names. Templates call `octicon` for every icon. `ai_modality` in `partials/ai.html` renders Octicons. `base.html` still defines the old stroke icons `icon_sidebar`, `icon_search`, `icon_more`, `icon_plus`, `icon_file` and `icon_device`, but no template calls them.

Sizes:

- 16px inside every control, row, menu item, tab, label, banner and inline message.
- 24px as the Blankslate graphic in narrow containers and in the phone keyboard bar, where the 44px target leaves room.
- 12px only in the small InlineMessage and the small Label, where Primer uses the filled variant.
- The icon colour is `currentColor`, set by the component. Leading icons in ActionList and NavList use `--fgColor-muted`.

### 4.3 Icon Map

`ICON_MAP` in `ui.js` maps these names.

| Markspace name | Octicon | Size | Note |
| --- | --- | --- | --- |
| `sidebar` | `sidebar-collapse` when the sidebar is open, `sidebar-expand` when closed | 16 | |
| `rail` | `sidebar-expand` when the rail is closed, `sidebar-collapse` when open, both mirrored with `transform: scaleX(-1)` | 16 | Octicons has no right-side pane icon |
| `search` | `search` | 16 | |
| `plus` | `plus` | 16 | |
| `minus` | `dash` | 16 | |
| `more` | `kebab-horizontal` | 16 | Primer's progressive disclosure pattern uses the kebab for menus |
| `star` | `star` | 16 | |
| `starFill` | `star-fill` | 16 | Colour `--fgColor-attention` |
| `info` | `info` | 16 | |
| `alert` | `alert` | 16 | `alert-fill` at 12px |
| `check` | `check` | 16 | `check-circle-fill` at 12px |
| `close` | `x` | 16 | |
| `home` | `home` | 16 | |
| `clock` | `clock` | 16 | |
| `users` | `people` | 16 | |
| `user` | `person` | 16 | |
| `trash` | `trash` | 16 | |
| `settings` | `gear` | 16 | |
| `edit` | `pencil` | 16 | |
| `link` | `link` | 16 | |
| `copy` | `copy` | 16 | After a copy, `check` in `--fgColor-success` for 2s, as Primer's success guidance shows for copy buttons |
| `move` | `file-moved` | 16 | |
| `folder` | `file-directory-fill`, `file-directory-open-fill` when expanded | 16 | TreeView leading visual, colour `--treeViewItem-leadingVisual-iconColor-rest` |
| `history` | `history` | 16 | |
| `comment` | `comment` | 16 | |
| `share` | `share` | 16 | |
| `file` | `file` | 16 | Notes use `note` in the tree. Plain files use `file` |
| `files` | `paperclip` | 16 | Files rail tab and attachments |
| `image` | `image` | 16 | |
| `video` | `video` | 16 | |
| `audio` | `unmute` | 16 | |
| `archive` | `file-zip` | 16 | |
| `duplicate` | `duplicate` | 16 | |
| `child` | `file-added` | 16 | New child page |
| `rename` | `pencil` | 16 | |
| `signout` | `sign-out` | 16 | |
| `download` | `download` | 16 | |
| `upload` | `upload` | 16 | |
| `retry` | `sync` | 16 | |
| `back` | `arrow-left` | 16 | |
| `chevronDown` | `chevron-down` | 16 | |
| `chevronRight` | `chevron-right` | 16 | |
| `grid` | `apps` | 16 | |
| `list` | `list-unordered` | 16 | |
| `filter` | `filter` | 16 | |
| `eye` | `eye` | 16 | |
| `lock` | `lock` | 16 | |
| `key` | `key` | 16 | |
| `globe` | `globe` | 16 | |
| `sparkle` | `sparkle` | 16 | AI actions. `copilot` is GitHub's product mark and is not used |
| `cloud` | `cloud` | 16 | |
| `cloudOff` | `cloud-offline` | 16 | |
| `sun` | `sun` | 16 | |
| `moon` | `moon` | 16 | |
| `tag` | `tag` | 16 | |
| `hash` | `hash` | 16 | |

`IC` in `editor.js`, `ICONS` in `panels.js` and the template icons map these names.

| Markspace name | Octicon | Size | Note |
| --- | --- | --- | --- |
| `IC.focus` | `screen-full` | 16, 24 in the keyboard bar | |
| `IC.unfocus` | `screen-normal` | 16 | |
| `IC.typewriter` | `typography` | 16 | |
| `IC.bullet` | `list-unordered` | 24 in the keyboard bar | |
| `IC.task` | `tasklist` | 24 in the keyboard bar | |
| `IC.link` | `link` | 16, 24 | |
| `IC.image` | `image` | 16, 24 | |
| `IC.undo` | `undo` | 24 | |
| `IC.sparkle` | `sparkle` | 16, 24 | |
| `IC.strike` | `strikethrough` | 16, 24 | |
| `IC.code` | `code` | 16, 24 | |
| `IC.copy`, `ICONS.copy` | `copy` | 16 | |
| `IC.more` | `kebab-horizontal` | 16, 24 | |
| `IC.comment` | `comment` | 16, 24 | |
| `ICONS.close` | `x` | 16 | |
| Keyboard bar text glyphs B and I | `bold`, `italic` | 24 | Heading uses `heading`, quote `quote`, numbered list `list-ordered` |
| `icon_device` | `device-desktop` | 16 | |
| `ai_modality` text | `typography` | 16 | |
| `ai_modality` image | `image` | 16 | |
| `ai_modality` file | `file` | 16 | |
| `ai_modality` audio | `unmute` | 16 | |
| `ai_modality` other | `ai-model` | 16 | |
| Upload state `done` | `check-circle-fill` | 16 | `--fgColor-success` |
| Upload state `failed`, `error` | `alert-fill` | 16 | `--fgColor-danger` |
| Upload state `queued` | `clock` | 16 | `--fgColor-muted` |
| Space and note emoji glyphs | unchanged | | User content, not icons |

## 5. Components

Each subsection maps Markspace elements to a Primer component and gives the visual and behavioural spec. Values name tokens. The theme files supply light and dark values, and section 2.4 supplies the accent. A pixel value is given where Primer's CSS uses a fixed value. The mapping tables name Markspace classes and helpers, including earlier class names that the Primer component replaced.

### 5.0 Rules for Every Component

| Rule | Spec |
| --- | --- |
| Markup | Native elements first. `button` for actions, `a href` for navigation, `input`, `select`, `textarea`, `dialog` and `details`. ARIA roles only where the Primer component uses them |
| Focus ring | `:focus-visible { outline: 2px solid var(--focus-outlineColor); outline-offset: -2px; box-shadow: none; }` and `:focus:not(:focus-visible) { outline: solid 1px transparent; }`. The ring is never removed without a replacement |
| Focus ring on emphasis fills | Primary buttons add `box-shadow: inset 0 0 0 3px var(--fgColor-onEmphasis)` inside the outline |
| Focus ring offset | -2px on buttons and icon buttons. 0 on ActionList, NavList and TreeView items, plain links, checkboxes and radios. -1px on SegmentedControl segments. 2px on link buttons |
| Focus on text inputs | `border-color: var(--focus-outlineColor); outline: none; box-shadow: inset 0 0 0 1px var(--focus-outlineColor)` |
| Forced colours | `@media (forced-colors: active) { *:focus, *:focus-visible { outline: solid 1px transparent; } }`, so the system draws the outline |
| Target size | Every target is at least 24 × 24px, the WCAG 2.5.8 minimum Primer requires. On narrow viewports and under `(pointer: coarse)` every tap target is at least 44 × 44px (`--control-minTarget-coarse`). A control that stays visually 32px gets an invisible `::before` hit area, centred with `top: 50%` and `transform: translateY(-50%)`, as in Primer CSS's `minTouchTarget` mixin. The app header, page actions, dialog footers and the keyboard bar use the large size (40px) on narrow viewports, as Primer's responsive guidance recommends |
| Hover | ActionList, NavList and TreeView hover fills sit inside `@media (hover: hover)` |
| Disabled | Primer discourages disabled controls. An action the person cannot use is hidden. A control that must stay visible uses `aria-disabled="true"` and stays focusable. A form submit button is never disabled. A control broken by an outage or by being offline uses the inactive style and explains why |
| Accessible names | Verb and noun, unique on the page, starting with the visible text, such as "Delete Design notes". Icon-only controls use `aria-label` |
| Tooltips | Only IconButtons and icon-only SegmentedControl segments get a Tooltip (5.6). `title` attributes are removed everywhere |
| Links | Links in running text and in rendered markdown are underlined. UI links, such as a row title, may omit the underline when their position and weight show they are links, and gain an underline on hover |

### 5.1 Shell and Navigation

#### Layout

| Viewport | Structure |
| --- | --- |
| wide, 1400px and up | App header. SplitPageLayout with the sidebar pane at the start and the content. On note and file routes, the rail is a PageLayout pane at the end. Primer allows three columns only in this range |
| regular, 768px to 1399px | App header and at most two columns. The sidebar pane and the content show by default. Opening the rail hides the sidebar pane, and the header's sidebar button then opens the sidebar as a left side sheet. Closing the rail restores the sidebar pane |
| narrow, below 768px | App header and one column. The sidebar is a left Dialog side sheet. The rail is a full-screen Dialog |

| Property | Value |
| --- | --- |
| Scrolling | The document scrolls. `html` has `height: 100%` and no `overflow` rule. `body`, the layout root and the content region have no fixed height and no `overflow` rule. From 768px each pane is `position: sticky; top: 0; max-height: 100vh; overflow: auto` and scrolls inside itself. Below 768px no pane is sticky |
| Minimum height | The layout root has `min-height: 100vh`, and `min-height: 100dvh` under `@supports (height: 100dvh)`, so panes reach the bottom on short pages |
| Note width | The note fills the main column up to 1280px, side padding included. The side padding is 16px below 768px, 24px from 768px and 32px from 1012px. The note view, the editor and the server note page share these values |
| Scroll region names | A pane that overflows has `tabindex="0"`, `role="region"` and an `aria-label` ("Sidebar", "Page details") |
| Landmarks | One `header` (the app header), one `main` (the content), `nav` for each NavList, `aside` for the rail |
| Skip links | "Skip to content" and "Skip page tree", first in the document. Each is visible on focus as a Button default medium 8px from the top start corner, at `--zIndex-skipLink` |
| Sticky bars and focus | `html { scroll-padding-top }` equals the height of any sticky bar, so a focused element never sits under it |

#### App Header

| Property | Value |
| --- | --- |
| Element | `<header>`, static. It scrolls away with the page, as Primer's layout foundations require |
| Size | 64px tall. Inline padding follows the layout padding, 16px below 1012px and 24px from 1012px. Items sit 8px apart |
| Surface | bg `--bgColor-default`, 1px `--borderColor-muted` bottom border |
| Start | IconButton invisible medium that toggles the sidebar pane (`sidebar-collapse` or `sidebar-expand`), with `aria-expanded`, `aria-controls`, and a tooltip with the KeybindingHint `Ctrl \`. On narrow viewports it is IconButton invisible large `three-bars`, named "Open navigation" |
| Context region | The instance name as a link, a slanted separator as in Breadcrumbs, and the space name as a link, in 14px weight 600 `--fgColor-default`. Primer's context region is not a full breadcrumb and holds no buttons or menus. The note path moves to the PageHeader (5.2). On narrow viewports only the space name shows, truncated |
| Search | Button default medium (5.4). On narrow viewports IconButton invisible large `search`. Server pages render the same Button as a link to `/search` |
| Avatar sync badge | 5.7 |
| Uploads | Button invisible small with a leading `upload` and a CounterLabel, shown while uploads exist. It opens an AnchoredOverlay list |
| New page | Button primary medium with a leading `plus` and the label "New page". On narrow viewports IconButton primary large `plus`. The header shows New page only when the PageHeader of the view has no New page action, so a page never shows two |
| Account | Avatar 32px inside a 44px `button` with `aria-haspopup="true"`, which opens the account ActionMenu (5.5) |

#### Sidebar

| Property | Value |
| --- | --- |
| Component | SplitPageLayout pane with `position="start"`, `sticky`, `padding="normal"` and `divider="line"` |
| Width | 256px from 768px, 296px from 1012px |
| Padding | 16px below 1012px, 24px from 1012px. NavList and TreeView rows extend 8px into the padding, so their text lines up with the padding edge |
| Divider | 1px `--borderColor-default` on the end edge |
| Surface | bg `--bgColor-default` |
| Order, top to bottom | The space switcher. NavList "Main" with Starred (`star`), Recent (`clock`) and Shared with me (`people`). TreeView "Pages". A footer NavList with Trash (`trash`) and Space settings (`gear`). Outside a space, a NavList "Spaces" lists each space and ends with "Manage spaces" |
| Space switcher | SelectPanel trigger. Button invisible medium with `block` and `alignContent="start"`, a leading square Avatar 20px holding the space emoji, the space name, and a trailing `triangle-down` |
| Narrow viewports | Dialog `position="left"`, 100dvh tall, 320px wide with a cap of `calc(100vw - 64px)`, 12px radius on the end corners only, sliding in over 250ms. The header has the title "Navigation" and the close button, and the body holds the sidebar content. Following a link closes it |

#### NavList

| Part | Value |
| --- | --- |
| Markup | `<nav aria-labelledby="…">` with an `h2` heading, visually hidden where the context is clear, and a `<ul>` of `<li><a>` |
| Item | 32px tall. Padding 6px block and 8px inline, radius 6px, `--control-fgColor-rest`, 8px gap, background transition 33ms linear |
| Item on narrow viewports | 40px tall (10px block padding), with the 44px hit area |
| Label | 14px, line height 20px, weight 400, `--fgColor-default` |
| Leading visual | 16px Octicon in `--fgColor-muted`. Either every item in a list has one, or none does |
| Trailing visual | CounterLabel |
| Hover (`hover: hover`) | bg `--control-transparent-bgColor-hover` |
| Pressed | bg `--control-transparent-bgColor-active` |
| Current | `aria-current="page"` and bg `--control-transparent-bgColor-selected`. The label is weight 600 in `--control-fgColor-rest`. A bar `::after` sits at `top: 4px; left: -8px; width: 4px; height: calc(100% - 8px)` in `--borderColor-accent-emphasis`, with 6px radius |
| Focus | 2px `--focus-outlineColor` outline, offset 0 |
| Group heading | 12px weight 600, line height 18px, `--fgColor-muted`, padding 6px block and 16px inline. It is an `h3` under the list's `h2` |
| Groups | 8px between groups. A divider between groups is 1px `--borderColor-muted`, with 7px above and 8px below. Single items have no dividers |
| Items | Links only. Actions such as "New space" are Buttons outside the list |

#### TreeView (Page Tree)

| Part | Value |
| --- | --- |
| Markup | `<ul role="tree" aria-labelledby="pages-heading">`. Items are `<li role="treeitem" aria-level="n">`, parents carry `aria-expanded`, and children sit in `<ul role="group">`. The current note has `aria-current="true"`. Multi-selection sets `aria-multiselectable="true"` on the tree and `aria-selected` on items |
| Row | Min height 32px, 44px under `(pointer: coarse)` and on narrow viewports. Radius 6px. 14px text in `--fgColor-default` |
| Indent | A spacer of `(level - 1) × 8px`, 12px per level on coarse pointers |
| Chevron | 16px column (24px on coarse pointers). `chevron-right` when collapsed, `chevron-down` when expanded, in `--fgColor-muted`, hover bg `--control-transparent-bgColor-hover`. A click on the chevron only toggles |
| Leading visual | 16px. Folders use `file-directory-fill` or `file-directory-open-fill` in `--treeViewItem-leadingVisual-iconColor-rest`. Notes use `note` or their emoji. Files use the type icons from 4.3. Every node has one |
| Label | One line with an ellipsis. The full name shows in the item's PageHeader |
| Content padding | 8px inline, 8px gap between visual and label |
| Hover | bg `--control-transparent-bgColor-hover` |
| Current | bg `--control-transparent-bgColor-selected`, plus a 4 × 24px bar at `left: -8px`, vertically centred, in `--fgColor-accent` with 6px radius |
| Selected (multi-select) | bg `--control-transparent-bgColor-selected` without the bar |
| Focus | `box-shadow: inset 0 0 0 2px var(--fgColor-accent)` on the focused row |
| Level lines | 1px `--borderColor-muted` per level. On hover-capable devices they stay transparent until the tree is hovered or holds focus. On coarse pointers they always show |
| Interactive content | None besides the chevron. Actions open from the context menu, Shift+F10, the Menu key, and the item's PageHeader menu |
| Loading children | Skeleton rows 32px tall, with label widths cycling through 67%, 47%, 73%, 64% and 50%. When the count is unknown, one row reads "Loading…" with a Spinner small |
| Load error | A Dialog "Couldn't load pages" with Retry. Closing it returns focus to the parent row, and Retry moves focus to the loading row |
| Empty folder | A non-interactive row "No pages" in 12px `--fgColor-muted` at the child indent |
| Rename | The row swaps its label for a TextInput small with Save and Cancel as Buttons small (6.1) |
| Drag and drop | Kept from Markspace. The drop target row shows a 2px inset `--borderColor-accent-emphasis` ring. Move to and Copy to in the menu give the keyboard route, as Primer requires for gestures |

| Key | Action |
| --- | --- |
| Down, Up | Next or previous visible row, without expanding |
| Right | Expands a closed parent, or moves to the first child |
| Left | Collapses an open parent, or moves to the parent |
| Home, End | First or last visible row |
| Enter | Opens the item. Without a page reload, focus then moves to the item's `h1` |
| Printable characters | Type-ahead, case insensitive, 300ms timeout, wrapping, skipping collapsed rows |
| Tab | Leaves the tree. The tree has one tab stop and returns focus to the last focused row |
| F2, Delete, Ctrl+C, Ctrl+X, Ctrl+V | Markspace commands, active only while the tree has focus |

#### Rail Tabs

| Part | Value |
| --- | --- |
| Component | UnderlinePanels, which switches content without changing the URL |
| Markup | `role="tablist"` with `aria-label="Page details"`. Tabs are `role="tab"` buttons with `aria-selected` and `aria-controls`. Panels are `role="tabpanel"` with `aria-labelledby` and `tabindex="0"` |
| Strip | Min height 48px, padding 8px top and 16px inline, `box-shadow: inset 0 -1px var(--borderColor-muted)`, `overflow-x: auto; overflow-y: hidden`, 8px between tabs. Sticky at the top of the rail pane on bg `--bgColor-default` |
| Tab | 32px tall, padding 6px block and 8px inline, 14px text with 20px line height in `--fgColor-default`, radius 6px, 8px bottom margin |
| Labels | Text labels Outline, Comments, Suggestions, History, Share, Files, AI and Info. No icons, as UnderlineNav hides icons in containers narrower than 768px |
| Hover (`hover: hover`) | bg `--bgColor-neutral-muted`, 120ms ease-out |
| Selected | Label weight 600 and a 2px underline `::after` in `--underlineNav-borderColor-active` along the strip's bottom edge. A hidden weight-600 copy of the label reserves its width |
| Focus | `outline: 2px solid transparent; box-shadow: inset 0 0 0 2px var(--fgColor-accent)` |
| Counter | CounterLabel 8px after the label, included in the tab's accessible name |
| Keys | Left and Right move and wrap. Home and End go to the ends. Activation is manual. Enter or Space shows the panel, because most panels load from the network |
| Tab order | Tab enters on the selected tab. The next Tab moves into the panel |

#### Rail Pane

| Property | Value |
| --- | --- |
| Component | PageLayout pane with `position="end"`, `width="large"`, `sticky`, `padding="normal"`, `divider="line"` and `aria-label="Page details"` |
| Width | 256px from 768px, 320px from 1012px |
| Panel header | Inside each tab panel, an `h2` at title small (16px weight 600) names the panel through `aria-labelledby`. It is visually hidden, because the selected tab shows the same label. The header keeps at most one Button small action at the end, with 8px below it. The editor drawer outside the shell shows the `h2` |
| Toggle | IconButton invisible medium `sidebar-expand`, mirrored, in the PageHeader. It toggles the pane on regular viewports, and the choice is stored per device |
| Narrow viewports | Dialog `position="fullscreen"`, because Comments, Share and AI hold inputs. The Dialog title is "Page details" on every tab. It does not repeat the selected tab's name, because `test/browser` fails a Dialog title that repeats a tab name. The UnderlinePanels strip sits directly under the Dialog header as its subheader. The PageHeader IconButtons Comments (with its count) and Details (`info`) open it at the matching tab |

#### UnderlineNav

UnderlineNav holds tabs that change the URL, such as search scopes. It shares the rail tab strip values, with these differences.

| Part | Value |
| --- | --- |
| Markup | `<nav aria-label="…">` preceded by a visually hidden `h2`, then a `<ul role="list">` of links. The current link has `aria-current="page"`. Tab and Enter only, no arrow keys |
| Wrapper | `container-type: inline-size`. The list is clipped to 48px high with `flex-wrap: wrap` |
| Icons | Leading 16px icons in `--fgColor-muted` with 8px end margin, hidden when the nav container is narrower than 768px |
| Overflow | Items pushed onto the clipped row get `visibility: hidden` and `aria-hidden="true"` and appear in a "More" ActionMenu. Its trigger is a Button invisible labelled "More" plus a visually hidden " items", after a 1 × 24px `--borderColor-muted` divider with 4px side margins. When the current item is in the menu, the trigger shows the underline and weight 600 and is named "More items, including current item" |

#### Breadcrumbs

| Part | Value |
| --- | --- |
| Used for | The note and folder path in the PageHeader on regular and wide viewports. It replaces `.crumbs` in the header and `.breadcrumb` in the note view |
| Markup | `<nav aria-label="Breadcrumbs"><ol>`. Each item is a link, and the last item is the current page with `aria-current="page"` |
| Text | 14px. Links in `--fgColor-link`, underlined on hover. The current item in `--fgColor-default` |
| Separator | `::after` on every item except the last: `height: 0.8em; margin: 0 0.5em; border-right: 0.1em solid var(--fgColor-muted); transform: rotate(15deg)` |
| Focus | 2px outline, offset 2px, radius `--borderRadius-small` |
| Overflow | A path longer than 4 items collapses its middle items into an ActionMenu and keeps the root. The menu overlay is 192px to 320px wide |
| Narrow viewports | Replaced by the PageHeader parent link (5.2) |

### 5.2 Page Structure

#### Layout per Route

| Routes | Layout |
| --- | --- |
| Space home, note, folder, files view, file view, search, tag, trash, starred, recent, shared | The 5.1 layout. The content is `large` (1012px) and centred. Notes fill the main column up to 1280px instead, as the 5.1 row "Note width" states |
| Settings (`/settings/*`), administration (`/admin/*`) | SplitPageLayout whose pane is a NavList of the section's pages instead of the notes sidebar. The pane starts with a parent link back to the notes. Content width `large` |
| Spaces list, space settings | The notes layout with the space's sidebar |
| Sign in, sign up, setup, public link password | Interstitial page: one column, 320px wide, centred, with the instance name above the PageHeader |
| Public note, public item | PageLayout with `containerWidth="xlarge"` (1280px) and no pane. Note text at `--ms-measure` |
| Error, offline | Blankslate spacious (5.9), centred in the content |

On narrow viewports, settings and administration follow Primer's parent and detail pattern. `/settings` and `/admin` show the NavList as the page. A detail page hides the pane and shows a PageHeader parent link to the index. There are no top tab strips. `settings_nav` in `partials/settings.html` and `admin_nav` in `partials/admin.html` render the NavLists.

#### PageHeader

| Part | Value |
| --- | --- |
| Markup | A wrapper holding the page `h1`. The app header is already the page's `banner` landmark, and Primer allows one per page, so the PageHeader has no landmark role |
| Grid | Areas `context-area`, then `leading-action breadcrumbs title-area trailing-action actions`, then `description`, then `navigation`. Columns `auto auto auto auto 1fr` |
| Title, medium (default) | 20px, weight 600, line height 1.625. App pages, folders and files |
| Title, large | 32px, weight 400, line height 1.5. Note titles on regular and wide viewports. Narrow viewports use medium |
| Title, subtitle | 20px, weight 400, line height 1.625. A section title in a page that already has a title |
| Context area | Narrow viewports only. Flex row, 8px gap, 8px bottom padding, 14px text. It holds the parent link: an `arrow-left` Octicon and the parent's name, 8px apart, as a link to the parent |
| Breadcrumbs | Regular and wide viewports. The grid's breadcrumbs slot, with 8px end padding |
| Leading visual | The note's emoji or the file type Octicon. Visible on every viewport |
| Trailing visual | Label "Read only" or "Shared", and the AvatarStack of people with access |
| Actions | Flex row with 8px gap, 8px start padding and `min-width: max-content`, aligned to the end and centred on the title's first line |
| Actions on narrow viewports | The page's primary action stays as Button large. Every other action moves into an IconButton invisible large `kebab-horizontal` with an ActionMenu, as in Primer's overflow pattern |
| Description | 8px top padding, 14px `--fgColor-muted`, flex row with 8px gap. On notes it holds the word count, "Updated" with RelativeTime, the author, and the access Label |
| Border | List and settings pages set `hasBorder`: a 1px `--borderColor-default` bottom border with 8px padding above it. Note pages have no border |
| Spacing below | 16px, 24px from 1012px |

`.page-head`, `.page-head-row`, `.fallback-head`, `.settings-intro` and the file view header become PageHeader. `.note-top` is a plain row in the page flow, and phones have no fixed note action bar.

#### Section Headings

`.list-head`, `.form-block-head`, `.ms-section-head`, `.section-title`, `.ms-panel-head` and `panelShell` titles become one pattern.

| Part | Value |
| --- | --- |
| Heading | `h2`, or `h3` inside a section, at title small: 16px weight 600, line height 1.5 |
| Count | CounterLabel 8px after the heading text |
| Description | 14px `--fgColor-muted`, 4px below the heading |
| Actions | Button small or IconButton small, aligned to the end, 8px apart |
| Settings sections | PageHeader in the `subtitle` variant (20px, weight 400, line height 1.625) as an `h2`, with `hasBorder`: a 1px `--borderColor-default` bottom border and 8px padding above it, then 16px before the form. Primer describes the subtitle variant for "when a title is already present in the page, such as in a Split page layout". Primer CSS no longer ships its older Subhead component |

#### Boxes and Rows

Primer's current packages have no Box component. `@primer/css` 22.3.1 ships only the `.Box--overlay` rules, and Primer React has no Box styles. Markspace builds its boxes from the same tokens that Primer's DataTable and Banner use.

| Part | Value |
| --- | --- |
| Box | bg `--bgColor-default`, 1px `--borderColor-default`, radius `--borderRadius-medium` (6px), no shadow |
| Box header | Padding 16px, bg `--bgColor-muted`, 1px `--borderColor-default` bottom border, top radii 6px. Holds a section heading and its actions |
| Box body | Padding 16px |
| Row | Padding 16px (8px 16px in rail panels and other narrow containers), a 1px `--borderColor-muted` top border between rows, and none above the first row |
| Row layout | Grid `auto minmax(0, 1fr) auto` with 12px column gap. Leading visual 16px Octicon or 32px Avatar or thumbnail. Title 14px weight 600 `--fgColor-default`, as a link when the row opens something, underlined on hover. Metadata 12px `--fgColor-muted` with RelativeTime. Optional snippet 14px `--fgColor-muted`. Trailing controls aligned to the end with 8px gap |
| Row hover | bg `--control-transparent-bgColor-hover` when the whole row is a link. A stretched `::after` on the title link covers the row, and trailing controls sit above it |
| Row focus | The row shows the 2px `--focus-outlineColor` outline at -2px offset when its title link has `:focus-visible` |
| Current row | bg `--control-transparent-bgColor-selected` with `aria-current` on the link |
| Danger box | Border `--borderColor-danger-emphasis`. Header title `--fgColor-danger`. Each row holds one danger Button |
| Footer | Padding 16px, 1px `--borderColor-default` top border |

| Markspace element | Box |
| --- | --- |
| `.panel`, `.card`, `.tile`, `.ms-card`, `.ms-thread` | Box with a body |
| `.card-grid` space cards | A grid of Boxes, `repeat(auto-fill, minmax(220px, 1fr))` with 16px gap. The whole card is a link. Hover shows border `--borderColor-emphasis` |
| `.list-rows`, `.rows`, `.note-row`, `.hit-list`, `.file-rows`, `.upload-list`, `.ms-cards` | Box with rows |
| `.info-list` | Box with rows, each a `dt` in 14px `--fgColor-muted` and a `dd` in 14px `--fgColor-default`, 12px apart, padding 8px 16px |
| `.danger-zone`, `.form-block.is-danger`, `.danger-section` | Danger box |
| `.usage-tiles .tile` | Box body with the value at 20px weight 600 and the label at 12px `--fgColor-muted` |
| `.form-block` | Not a Box. Forms sit under a section heading (5.3) |
| `.secret-box` | Banner success (5.7) |

#### DataTable

| Part | Value |
| --- | --- |
| Used for | Admin users, members, API tokens, AI providers, models, usage by model, usage by person, recent calls, budgets |
| Header | Title 14px weight 600, which names the table through `aria-labelledby`. Optional subtitle 12px through `aria-describedby`. Actions aligned to the end with 8px gap. 8px between header and table. When the PageHeader already names the table, the table references it and no title repeats |
| Table text | 12px, line height 20px |
| Cells | Padding 8px 12px. The first and last cell of each row use 16px inline padding |
| Borders | 1px `--borderColor-default` under every cell, above the header row, at the start of the first column and at the end of the last. 6px radius on the four corner cells |
| Header row | bg `--bgColor-muted`, weight 600, `--fgColor-muted`. A sorted column's header uses `--fgColor-default` |
| Row header | First cell `<th scope="row">`, weight 600, `--fgColor-default`. Avatar and name, with the email below in 12px `--fgColor-muted` |
| Row hover | Cell bg `--control-transparent-bgColor-hover` |
| Numbers | End aligned, `font-variant-numeric: tabular-nums` |
| Empty cells | Blank |
| Row actions | Last column, with a visually hidden header "Actions" and the width of its content. One action is a Button small. Two or more are an IconButton invisible small `kebab-horizontal` with an ActionMenu, named "Actions: {row header}" |
| Sorting | A `button` inside the `th`, with a `sort-asc` or `sort-desc` Octicon that shows on header hover, on focus and for the active column. The `th` has `aria-sort` |
| Loading | Skeleton rows matching column widths and alignment, with text widths cycling through 85%, 67.5%, 80%, 60% and 75%. No announcement when data arrives within 750ms |
| Empty | A Blankslate replaces the table |
| Pagination | From 20 rows. The footer sits below the table: flex, `justify-content: space-between`, 16px gap, padding 8px 16px, 1px border without a top edge, bottom radii 6px. Range text in 12px `--fgColor-muted`, such as "1 through 20 of 84". Page buttons at least 32 × 32px, padding 8px 6px, radius 6px, 14px. The current page is bg `--bgColor-accent-emphasis` with `--fgColor-onEmphasis` and `aria-current="true"`. Previous and Next are `--fgColor-muted`, or `--fgColor-accent` when a page exists. A page change announces the new range in the status region |
| Narrow viewports | Less important columns hide, and Status and Last sign in fold into the row header's description. A table still wider than the screen scrolls sideways inside a wrapper with `tabindex="0"`, `role="region"` and an `aria-label` such as "Users table" |

#### Pagination (Server Pages)

`partials/pagination.html` lists page by URL, so they use Primer's Pagination.

| Part | Value |
| --- | --- |
| Markup | `<nav aria-label="Pagination">` at the end of the content. Not rendered when one page holds everything |
| Page link | Min width 32px, height 32px, padding 8px 6px, 4px end margin, line height 1, radius 6px, `--fgColor-default`, named "Page 3" |
| Hover and focus | bg `--control-transparent-bgColor-hover`. Focus outline 2px `--bgColor-accent-emphasis`, offset -2px |
| Current | bg `--bgColor-accent-emphasis`, text `--fgColor-onEmphasis`, `aria-current="page"`. Focus adds `box-shadow: inset 0 0 0 3px var(--fgColor-onEmphasis)` |
| Previous and Next | `--fgColor-accent`, with `chevron-left` and `chevron-right` at 1em and a 4px gap, named "Previous Page" and "Next Page". At either end they show in `--fgColor-disabled` and are not links |
| Pages shown | The first page, the last page, and 2 pages on each side of the current page, with an ellipsis for gaps. Without a total from the server, only Previous and Next show |
| Spacing | 20px above, 15px below, centred |
| Narrow viewports | Page numbers stay visible, as Primer's reflow rule requires |

### 5.3 Forms

#### Mapping

| Markspace element | Primer component |
| --- | --- |
| `.field` with `.field-label`, `.field-hint` and `.field-error` | FormControl, vertical |
| `input` of type text, email, password, url, number, tel, date, time or search | TextInput |
| `textarea`, `.ms-textarea`, comment and reply boxes, ask bar input | Textarea |
| `select` in a form with a Save button | Select |
| `.check` checkbox in a form | Checkbox in a horizontal FormControl. Related checkboxes form a CheckboxGroup |
| `.ai-preset` radio cards, `.usage-toggle-options` radios | Radio in a RadioGroup |
| `.switch`, `.ai-switch`, `.ai-toggle-form` settings that apply at once | ToggleSwitch |
| `.ms-combo` and `.ai-add` searches with results | Autocomplete |
| `input.edit-in-place`, `.tree-rename` | TextInput small with Save and Cancel (6.1) |
| `.secret-row code`, `.share-url` | TextInput, monospace and read-only, with a trailing copy action |
| `.search-box` on the search page | TextInput large with a leading `search` visual |
| `.list-search` | TextInput medium with a leading `search` visual, submitted with Enter or a Button default "Search" |
| `.dropzone`, `.file-drop` | Drop area (below) |
| `.form-block` | A form inside a section (5.2), not a filled block |
| `.settings-section` two-column grid | One column: the section heading, then the stacked form |
| `.field-row` | Stacked FormControls. A name paired with a short value, such as an emoji icon, stays side by side from 544px |
| `.disclosure`, `.more-options` | Details (5.9) |
| `input[type="color"]` | The native colour input inside the TextInput box, 32px tall and 48px wide. Primer has no colour picker |

#### TextInput, Textarea and Select Box

| Property | Value |
| --- | --- |
| Box | `display: inline-flex`, or `display: flex; width: 100%` for block inputs. 8px gap between visuals and the input |
| Height | small min 28px, medium min 32px (default), large 40px |
| Padding | 12px inline on the inner input when there are no visuals. With a leading visual the box has 8px start padding, and with a trailing visual 8px end padding. Large with visuals uses 12px. Small uses `3px 8px` |
| Text | 14px, line height 20px, `--fgColor-default`. Small uses 12px. Monospace inputs use `--fontStack-monospace` |
| Text on iOS | 16px below 768px on iOS. Primer CSS's `.form-control` sets `$h4-size` (16px) inside `@supports (-webkit-touch-callout: none)`, which matches only iOS Safari, and returns to `$body-font-size` (14px) from its `md` breakpoint (768px). The comment reads "Ensures inputs don't zoom on mobile iPhone but are body-font size on iPad". Primer React's TextInput keeps 14px everywhere. Markspace follows Primer CSS, because iOS zooms the page when an input under 16px gains focus |
| Surface | bg `--bgColor-default`, 1px `--control-borderColor-rest`, radius 6px, shadow `--shadow-inset` |
| Placeholder | `--control-fgColor-placeholder`. Only an example value, never a label |
| Focus | `border-color: var(--borderColor-accent-emphasis); outline: 2px solid var(--borderColor-accent-emphasis); outline-offset: -1px` on `:focus-within` |
| Error | Border `--borderColor-danger-emphasis`. When focused, border and outline `--control-borderColor-danger`. `aria-invalid="true"` |
| Success | Border `--bgColor-success-emphasis`. `aria-invalid="false"` |
| Disabled | Text `--fgColor-disabled`, bg `--control-bgColor-disabled`, border `--control-borderColor-disabled`, no shadow, `cursor: not-allowed` |
| Leading and trailing visuals | 16px in `--fgColor-muted`. Not both on one input |
| Trailing action | IconButton invisible, 24px square (20px small, 28px large), padding 2px 4px, `--fgColor-muted`, `--fgColor-default` on hover and focus, 4px inline margin, and a 44px hit area on coarse pointers. Used for clear and copy |
| Loading | A Spinner small replaces the leading or trailing visual. A visually hidden live region announces it |
| Textarea | Inner padding 12px, `resize: vertical`. 7 rows by default for long text and 3 rows for replies. The ask bar and composers grow to 6 lines before scrolling. An optional character counter sits below in 12px `--fgColor-muted`, turning `--fgColor-danger` over the limit |
| Select | A native `select` with `appearance: none` inside the box, 1px margins, and a `triangle-down` Octicon placed 4px from the end, centred vertically, with `pointer-events: none`. One option is selected by default. Options are alphabetical unless their order carries meaning. A list of 6 options or fewer with room to show them uses a RadioGroup instead |
| Width | Matches the expected value. Names and emails fill the form column up to 440px. Short values such as the space icon are 104px |

#### FormControl

| Part | Value |
| --- | --- |
| Vertical layout | Flex column, `align-items: flex-start`, 4px between label, input, caption and validation message |
| Horizontal layout (checkbox, radio) | Flex row, 8px between control and label, label weight 400 |
| Label | 14px, weight 600, `--fgColor-default`, `<label for>`. At most 3 words, sentence case, no colon |
| Required | `*` after the label text with a 4px gap, `required` on the input, and `novalidate` on the form. Sign-in forms, where every field is required, omit the marker |
| Caption | 12px `--fgColor-muted`, linked with `aria-describedby`. Hidden while a validation message repeats what it says |
| Validation message | 12px weight 600 in a flex row. Errors are `--fgColor-danger` with a 12px `alert-fill`, success is `--fgColor-success` with a 12px `check-circle-fill`. The icon has 2px top margin and 4px end margin. Linked with `aria-describedby` |
| Validation motion | Fades and slides in from -100% Y over 170ms `cubic-bezier(0.44, 0.74, 0.36, 1)`. None under reduced motion |
| Spacing between controls | 16px (`--stack-gap-normal`) |
| Disabled label and caption | `--control-fgColor-disabled` |

#### Checkbox and Radio

| Part | Checkbox | Radio |
| --- | --- | --- |
| Box | 16 × 16px, 2px top margin, `appearance: none`, bg `--bgColor-default`, 1px `--control-borderColor-emphasis`, radius 3px | The same, with radius `--borderRadius-full` |
| Checked | bg and border `--control-checked-bgColor-rest`, and a 12 × 9 check mask at 75% in `--fgColor-onEmphasis`, revealed by `clip-path` over 80ms `cubic-bezier(0.65, 0, 0.35, 1)` after an 80ms delay | bg `--control-checked-fgColor-rest`, border `--control-checked-bgColor-rest` at 4px width |
| Indeterminate | A 10 × 2 dash mask, `aria-checked="mixed"` | None |
| Disabled | bg `--control-bgColor-disabled`, border `--control-borderColor-disabled`. When checked, bg `--control-checked-bgColor-disabled` and mark `--control-checked-fgColor-disabled` | The same tokens |
| Focus | 2px `--focus-outlineColor` outline, offset 2px | The same |
| Group | `<fieldset>` with a visible `<legend>` at 14px weight 600, and one validation message for the whole group | `<fieldset>` and `<legend>`. Vertical only. The most likely option is checked by default, and a "None" option exists where no choice is valid |
| Saving | Only on submit | Only on submit |
| Hit area | Control and label together at least 24 × 24px, 44px on narrow viewports | The same |

#### ToggleSwitch

| Part | Value |
| --- | --- |
| Used for | One setting outside a larger form that applies at once, such as AI features on or off, a model enabled, or a file kept offline. A form with a Save button uses a Checkbox instead |
| Track | Medium 64 × 32px, small 48 × 24px, radius 6px, 1px border |
| Off | bg `--controlTrack-bgColor-rest`, border `--controlTrack-borderColor-rest`. Hover and focus bg `--controlTrack-bgColor-hover`, pressed `--controlTrack-bgColor-active` |
| On | bg `--control-checked-bgColor-rest`, border `--control-checked-borderColor-rest`. Hover bg `--control-checked-bgColor-hover`, pressed `--control-checked-bgColor-active` |
| Knob | Half the track width, inset 1px, bg `--controlKnob-bgColor-rest`, 1px `--controlKnob-borderColor-rest`, radius 4px. When on, `translateX(calc(100% - 2px))` and border `--controlKnob-borderColor-checked` |
| Track icons | A line on the on side in `--control-checked-fgColor-rest`, and a circle on the off side in `--controlTrack-fgColor-rest` |
| Status text | "On" or "Off", 14px (12px small), 8px from the track, at the start by default. Both words are rendered, with the hidden one at zero height, so the width stays fixed |
| Motion | 80ms `cubic-bezier(0.5, 1, 0.89, 1)` on background, border and transform. Removed under reduced motion |
| Focus | `outline: var(--focus-outline); outline-offset: 3px` |
| Markup | `<button aria-pressed>`, labelled by the setting's visible label through `aria-labelledby`, with the caption through `aria-describedby`. The label names the setting, such as "AI features", not its state |
| Layout | Label and caption at the start, the switch at the far end of the row |
| Disabled | bg `--controlTrack-bgColor-disabled`, transparent border, no motion |
| Loading | A Spinner small 8px after the switch, announced in a live region |
| Hit area | `::before` 44px tall on coarse pointers |

#### Autocomplete

| Part | Value |
| --- | --- |
| Input | TextInput with a visible label, `role="combobox"`, `aria-expanded`, `aria-controls` and `aria-autocomplete="list"` |
| Menu | The ActionMenu overlay surface, 4px under the input and as wide as the input, max height `min(320px, 100dvh)`. It holds an ActionList `role="listbox"` of `role="option"` items, and the input carries `aria-activedescendant` |
| Filtering | Case-insensitive match on the typed value. Filtering is never turned off |
| Order | Most likely first, or alphabetical |
| Adding | When the typed value matches nothing and adding is allowed, the last item reads "Invite person@example.com" or "Add “value”" |
| Loading | A Spinner small, centred with 16px padding, while options load |
| Empty | "No matches" with 16px padding |
| Multi-select | Chosen values show as Tokens large inside the input box, which wraps onto more lines |
| Result count | Announced in the status region |
| Narrow viewports | Stays under the input, width `min(100%, 100vw - 32px)` |

#### Drop Area

Primer has no drop zone component. The drop area is built from Primer tokens.

| Part | Value |
| --- | --- |
| Box | 1px dashed `--borderColor-default`, radius 6px, bg `--bgColor-muted`, padding 16px, min height 96px, a centred column with 8px gap |
| Text | "Drop files here" in 14px `--fgColor-muted`, with a Button default small "Choose files" |
| Drag over | Border `--borderColor-accent-emphasis`, bg `--bgColor-accent-muted` |
| Error | Border `--borderColor-danger-emphasis`, with an InlineMessage critical below |
| Keyboard | The Button is the keyboard route. The box itself is not focusable |

### 5.4 Buttons

#### Mapping

| Markspace element | Primer component | Variant and size |
| --- | --- | --- |
| `.btn` | Button | default, medium |
| `.btn.small` | Button | default, small |
| `.btn-primary` | Button | primary, medium. At most one per page or button group, placed last |
| `.btn-quiet` | Button | invisible |
| `.btn.danger`, `.btn-danger` | Button | danger. Primer has one danger style, red text at rest and a red fill on hover |
| `.btn-block` | Button | `block`, width 100% |
| `.icon-btn`, `.ms-icon-btn`, `.fv-tool` | IconButton | invisible, medium |
| `.row-menu-btn`, `.tree-more`, `.ms-card-icon`, `.ms-c-icon`, `.upload-actions .icon-btn` | IconButton | invisible, small, `kebab-horizontal` for menus |
| `.cm-ms-code-copy` | IconButton | invisible, small, `copy` |
| `.cm-ms-media-more` | IconButton | default, small, over the image |
| `.ms-kbar-btn` | IconButton | invisible, large, 24px icon |
| `.cm-ms-ai-btn` | Button | default, small. `.is-accept` is primary, small |
| `.ms-done` | Button | primary, medium. Large on narrow viewports |
| `.search-trigger` | Button | default, medium, `alignContent="start"`, leading `search`, trailing KeybindingHint `Ctrl K`. On narrow viewports an IconButton invisible large |
| `.sidenav-action` ("Manage") | Button | invisible, small |
| `.status-action`, `.undo-btn`, `.toast-action` | Button | invisible, small, inside the Banner or InlineMessage that replaces them (5.7) |
| `.add-toggle > summary` ("New space", "Create account") | Button | default, medium, leading `plus` |
| `.pagination` Previous and Next | Pagination, section 5.2 | |
| `.comment-float-btn`, `.cm-ms-bubble-btn` | Button | invisible, small, inside the selection toolbar overlay (5.6) |
| `.toast-close` | removed with toasts | |

#### Sizes

| Size | Height | Padding inline | Gap | Font | Icon |
| --- | --- | --- | --- | --- | --- |
| small | 28px `--control-small-size` | 8px | 4px | 12px, weight 500, line height 1.625 | 16px |
| medium (default) | 32px `--control-medium-size` | 12px | 8px | 14px, weight 500, line height 1.5 | 16px |
| large | 40px `--control-large-size` | 16px | 8px | 14px, weight 500, line height 1.5 | 16px |

All buttons have `display: inline-flex`, `align-items: center`, a 1px border, `--borderRadius-medium` (6px), `font-family: inherit`, `white-space: nowrap` on the label, `user-select: none` and the 80ms transition from section 3.6. A button with a keyboard hint inside has 6px (medium) or 8px (large) end padding. A trailing `triangle-down` action has `margin-right: -4px`. IconButton is square, 28, 32 or 40px wide, with `padding: 0` and the icon centred.

#### Variants

| Variant | Rest | Hover | Active and `aria-expanded="true"` | Disabled |
| --- | --- | --- | --- | --- |
| default | bg `--button-default-bgColor-rest`, border `--button-default-borderColor-rest`, text `--button-default-fgColor-rest`, shadow `--button-default-shadow-resting`, icons `--fgColor-muted` | bg `--button-default-bgColor-hover`, border `--button-default-borderColor-hover` | bg `--button-default-bgColor-active`, border `--button-default-borderColor-active` | bg `--button-default-bgColor-disabled`, border `--button-default-borderColor-disabled`, text `--button-default-fgColor-disabled`, no shadow, `cursor: not-allowed` |
| primary | bg `--button-primary-bgColor-rest`, border `--button-primary-borderColor-rest`, text and icons `--button-primary-fgColor-rest`, shadow `--shadow-resting-small` | bg `--button-primary-bgColor-hover`, border `--button-primary-borderColor-hover` | bg `--button-primary-bgColor-active`, shadow `--button-primary-shadow-selected` | bg and border `--button-primary-bgColor-disabled`, text `--button-primary-fgColor-disabled` |
| danger | bg `--button-danger-bgColor-rest`, border `--button-danger-borderColor-rest`, text and icons `--button-danger-fgColor-rest` | bg `--button-danger-bgColor-hover`, border `--button-danger-borderColor-hover`, text and icons `--button-danger-fgColor-hover` | bg `--button-danger-bgColor-active`, text `--button-danger-fgColor-active`, shadow `--button-danger-shadow-selected` | bg `--button-danger-bgColor-disabled`, text `--button-danger-fgColor-disabled` |
| invisible | bg transparent, border transparent, text `--button-invisible-fgColor-rest`, icons `--button-invisible-iconColor-rest`, no shadow | bg `--button-invisible-bgColor-hover`, text `--button-invisible-fgColor-hover`, icons `--button-invisible-iconColor-hover` | bg `--button-invisible-bgColor-active` | text and icons `--button-invisible-fgColor-disabled` |
| link | no bg, border, padding or radius, `height: auto`, font size inherited, text `--fgColor-link` | underline | | text `--fgColor-disabled` |
| inactive (any variant) | bg and border `--button-inactive-bgColor`, text `--button-inactive-fgColor`, `cursor: auto`, no hover change. Stays focusable | | | |

IconButton in the default variant uses `--fgColor-muted` for its icon.

A counter inside a button is a CounterLabel (5.8) with 8px left margin.

#### Loading

A busy button keeps its label and width. A Spinner (5.9) at 16px replaces the leading visual, or the trailing visual when no leading visual exists. A button with no visuals hides the label with `visibility: hidden` and centres the Spinner in its place. The button gets `aria-disabled="true"` and keeps focus. A visually hidden `aria-live="polite"` region, present from page load, receives a message such as "Saving profile" while loading. `setBusy` in `ui.js` implements this and replaces the current `[aria-busy]` rule that hides the label with `color: transparent`.

#### Placement

| Place | Rule |
| --- | --- |
| Page form | Save at bottom left. Cancel to its right |
| Dialog and comment box | Buttons at bottom right. Cancel to the left of the primary button |
| Inline edit | Save and Cancel directly beside the input |
| Button group | Primary last. Never more than one primary together |
| Header of a list or page | Default buttons. The creation action of a page may be primary |

### 5.5 Menus and Pickers

#### Mapping

| Markspace element | Primer component |
| --- | --- |
| `openMenu` in `ui.js` (row, tree item, file and folder menus, right-click menus, keyboard bar "More formatting") | ActionMenu |
| `inlineMenu`, `.action-row`, `.action-item`, `.row-menu-inline` (phone action rows) | ActionMenu, full screen on narrow viewports. The inline rows are removed |
| `.row-menu[popover]` on server pages | ActionMenu markup with the `popover` attribute. `app.js` adds the keyboard behaviour below to every `[data-action-menu]` |
| Account menu (`details.menu` in `base.html`, `openUserMenu` in `shell.js`) | ActionMenu, trigger Avatar button. `.menu-head` becomes a group heading showing the name, with the email as its description |
| Theme choice (`.segmented` inside the account menu) | One icon-only SegmentedControl (2.3) on its own row below the sync group, after the label "Theme". Its list is a `role="group"` named "Theme" and each segment is a `menuitemradio` with `aria-checked`, so the menu's arrow keys reach it. Left and Right move between the segments. A choice keeps the menu open. In the full-screen menu below 768px each segment is at least 44px wide and 44px tall |
| Row role and access changers (`.member-list select`, share access) | ActionMenu single select. The trigger is Button small with the current value, such as "Can edit", and a trailing `triangle-down`. Choosing an item saves at once, as Primer's saving pattern prescribes for custom single-select menus |
| Space switcher (`.space-switch`) | SelectPanel, single select, with a filter input |
| AI model chip (`assistant.md`) | SelectPanel, single select, anchored |
| Quick switcher (`dialog.switcher`, `Ctrl K`) | Dialog, xlarge, `align="top"`, visually hidden title "Jump to", filter TextInput first, ActionList `role="listbox"` below (5.6) |
| Move to and Copy to destination (sidebar pick mode) | Dialog, large, with a TreeView of folders in single select and a footer with Cancel and "Move here" or "Copy here" |
| Share person search (`.ms-combo`, `.ms-results`), AI target search (`.ai-results`) | Autocomplete, section 5.3 |
| Slash menu, `[[` completion, `@` mentions (CodeMirror autocomplete) | Autocomplete list spec, anchored to the caret |

#### ActionMenu Overlay

| Property | Value |
| --- | --- |
| Surface | bg `--overlay-bgColor`, radius `--borderRadius-large` (12px), shadow `--shadow-floating-small`, `overflow: auto` |
| Width | min 192px, max `calc(100vw - 2rem)`. The content sets the width between them |
| Max height | `min(432px, 100dvh)` (large). Menus with 8 items or fewer use `min(320px, 100dvh)` |
| Position | 4px (`--overlay-offset`) outside the anchor's bottom edge, aligned to the anchor's start. Trailing kebab menus align to the end. When space runs out the overlay flips to the top, then to the side, and keeps 8px from the viewport edge |
| Layer | `--zIndex-dropdown` (200), or the top layer through the `popover` attribute |
| Enter | Opacity 0 to 1 in 200ms `cubic-bezier(0.33, 1, 0.68, 1)`, only under `prefers-reduced-motion: no-preference` |
| Narrow viewports | `position: fixed; inset: 0 auto auto 0; width: 100vw; height: 100dvh; border-radius: 0; padding-top: 36px; padding-bottom: env(safe-area-inset-bottom)`. An IconButton invisible medium with `x` and the name "Close" sits 8px from the top and right edges. It is hidden at 768px and wider |

#### ActionList Inside Menus and Lists

| Part | Value |
| --- | --- |
| List | `list-style: none; margin: 0; padding-block: 8px`. Items have `margin-inline: 8px`. 2px between items |
| Item | `border-radius: 6px`, bg transparent, grid content with `padding-block: 6px`, `padding-inline: 8px`, 8px gap between slots, `touch-action: manipulation`, background transition 33.333ms linear. Height 32px. The large size uses 10px block padding for 40px |
| Item on narrow viewports | Large size, 40px, with the 44px hit area from 5.0 |
| Label | 14px, line height 20px, weight 400, `--fgColor-default`, `word-break: break-word` |
| Description | 12px, line height 16px, `--fgColor-muted`. Block descriptions sit 4px under the label. Inline descriptions sit 8px beside it on the baseline. A label with a block description is weight 600 |
| Leading visual | 16px Octicon, `--fgColor-muted`, in a 20px-tall slot |
| Trailing visual | Keyboard hint or submenu `arrow-right`, `--fgColor-muted`, 14px text. Shortcut text matches the platform, "Ctrl+B" on Linux and Windows, "⌘B" on macOS |
| Hover (`hover: hover` only) | bg `--control-transparent-bgColor-hover` |
| Pressed | bg `--control-transparent-bgColor-active` |
| Focus | 2px `--focus-outlineColor` outline, offset 0 |
| Danger item | Label and icon `--control-danger-fgColor-rest`, hover bg `--control-danger-bgColor-hover`, pressed bg `--control-danger-bgColor-active`. Danger items go last, after a divider |
| Disabled item | All content `--control-fgColor-disabled`, no hover fill, `aria-disabled="true"` |
| Inactive item | Content `--fgColor-muted`, a second line in 12px `--fgColor-attention` saying why, such as "Needs a connection" |
| Checked item | `menuitemradio` or `menuitemcheckbox` with `aria-checked`. A `check` Octicon in the leading slot shows when checked |
| Divider | `role="separator"`, 1px `--borderColor-muted`, 7px above and 8px below |
| Group heading | 12px, weight 600, line height 18px, `--fgColor-muted`, padding 6px block and 16px inline. The group has `role="group"` and `aria-labelledby` pointing at the heading |

#### ActionMenu Behaviour

| Aspect | Rule |
| --- | --- |
| Trigger | `aria-haspopup="true"` and `aria-expanded`. Enter, Space, ArrowDown and ArrowUp open the menu |
| Roles | Container `role="menu"`. Items `menuitem`, `menuitemradio` or `menuitemcheckbox` |
| Opening | Focus moves to the first item |
| Keys | ArrowDown and ArrowUp move and wrap. Home and PageUp go to the first item, End and PageDown to the last. A typed letter moves to the next item starting with it. ArrowRight opens a submenu and ArrowLeft closes it |
| Activation | Enter or Space runs the item and closes the menu. Focus returns to the trigger unless the item navigates |
| Closing | Escape closes and returns focus to the trigger. Tab and Shift+Tab close the menu and move focus onward. A click outside closes |
| Content | Menu items only. No inputs, buttons or checkboxes. A menu that needs filtering is a SelectPanel |
| Icons | All items in a menu have leading icons, or none do |
| Empty | When a menu would have no available items, its trigger uses the inactive style |
| Context menu | Right-click on a tree row, list row or file card opens the same menu at the pointer. Shift+F10 and the Menu key open it from the keyboard |

#### SelectPanel

| Part | Value |
| --- | --- |
| Trigger | Button with the current value and a trailing `triangle-down`, `aria-haspopup="dialog"`. The accessible name holds the label and the value, such as "Space: Design notes" |
| Surface | The ActionMenu overlay surface, width `--overlay-width-small` (320px), max height `min(432px, 100dvh)`. `role="dialog"`, `aria-labelledby` pointing at the title |
| Header | Padding 8px 8px 0. Title 14px weight 600 with 8px left margin. Optional subtitle 12px `--fgColor-muted`. IconButton invisible small `x` "Close" at the end |
| Filter | TextInput medium (5.3) with 8px margin and a leading `search` icon |
| List | ActionList `role="listbox"`, items `role="option"` with `aria-selected`. Single select shows a `check` in the leading slot. Multi select shows a 16px checkbox with 3px radius, 1px `--control-borderColor-emphasis`, checked fill `--control-checked-bgColor-rest` |
| Focus | Real focus stays in the filter input. `aria-activedescendant` marks the active option, which shows the pressed fill and the 4px accent bar from NavList (5.1). A `role="status"` region announces the result count |
| Order | Selected items first on open and after the filter is cleared. The order does not change while the panel is open |
| Footer | Only in the modal variant. 1px `--borderColor-default` top border, 8px padding, Cancel and Save |
| Saving | Anchored single select applies on selection and closes. Anchored multi select applies when the panel closes |
| Empty | Centred column, 24px padding, 4px gap. Title 14px weight 600, body 12px `--fgColor-muted`. Default title "No items available" |
| Error | The same layout with an `alert` icon in `--fgColor-danger` and 8px below it |
| Loading | Centred Spinner medium with 16px gap to a 14px `--fgColor-muted` message |
| Narrow viewports | Full screen as the ActionMenu overlay. The footer shows Cancel and Save, and Save may stretch |

### 5.6 Dialogs, Overlays and Tooltips

#### Mapping

| Markspace element | Primer component |
| --- | --- |
| `openModal` (create forms, invite, token, image edit, recovered changes) | Dialog |
| `confirmDialog`, `inlineConfirm`, `.is-confirming` two-step buttons, `data-confirm` on server buttons | ConfirmationDialog for destructive and irreversible actions (section 6.2). Undo for the rest |
| `promptDialog` (rename, new folder, link text) | Inline edit with Save and Cancel for rename. Dialog, small, for new folder and link text |
| `.quickopen`, `dialog.switcher` | Dialog, xlarge, `align="top"` |
| `dialog.dialog`, `dialog.modal` | Dialog |
| `popover()` in `ui.js` | AnchoredOverlay, the ActionMenu surface with `role="dialog"` |
| Wikilink hover preview (`.ms-wiki-preview`) | AnchoredOverlay, width 320px, padding 16px, `role="dialog"`. Primer has no hovercard component. It stays off by default, as `ui.md` rule 5 requires |
| Selection toolbar on desktop (`.comment-float`, `.cm-ms-bubble`) | AnchoredOverlay without a role, holding a `role="toolbar"` of Button invisible small. Primer has no floating toolbar component, so only the surface values come from Primer |
| Rail on narrow viewports | Dialog, `position="fullscreen"` (5.1 Rail pane) |
| Sidebar drawer on narrow viewports | Dialog, `position="left"`, reserved by Primer for global navigation drawers |
| Every `title` attribute on a control | Tooltip on IconButtons, removed elsewhere |

#### Dialog

| Property | Value |
| --- | --- |
| Element | `<dialog aria-modal="true" aria-labelledby="…">` opened with `showModal()` |
| Width | small 296px, medium 320px, large 480px, xlarge 640px (default). Min width 296px. Max width `calc(100dvw - 64px)` |
| Height | auto (default), small 480px, large 640px. Max height `calc(100dvh - 64px)`. At viewport heights of 280px or less, the margins shrink to 12px |
| Surface | bg `--overlay-bgColor`, radius `--borderRadius-large`, shadow `--shadow-floating-small`, `display: flex; flex-direction: column` |
| Backdrop | `::backdrop { background: var(--overlay-backdrop-bgColor); }`, fading in over 200ms |
| Position | Centred. `align="top"` puts it 64px from the top. Every centred dialog keeps 16px from the viewport edges |
| Header | Padding 8px. Bottom line `box-shadow: 0 1px 0 var(--borderColor-default)`. Max height 35vh, scrolls when taller. Title block padding 6px 8px |
| Title | `h1`, 14px, weight 600, margin 0. Required. Visually hidden only for the quick switcher |
| Subtitle | 12px, weight 400, `--fgColor-muted`, 4px above. Referenced by `aria-describedby` |
| Close button | IconButton invisible medium `x` named "Close", 8px from the top and end edges. Required, never hidden |
| Body | Padding 16px, `flex-grow: 1`, `overflow: auto`. ActionList and search fields inside may bleed into the padding |
| Footer | Padding 16px, `display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end`. Buttons medium. A divider above the footer shows only while the body scrolls |
| Banner inside | Directly below the header, full width of the body |
| Enter | Scale 0.5 to 1 with a fade, 200ms `cubic-bezier(0.33, 1, 0.68, 1)`, under `prefers-reduced-motion: no-preference` |
| Page scroll | `body` gets `overflow: hidden` and a scrollbar-width padding while a dialog is open |

| Behaviour | Rule |
| --- | --- |
| Opening | Focus moves to the first interactive element, usually the close button. A dialog whose purpose is an input, such as the quick switcher, focuses the input |
| Focus | Tab cycles inside. The page behind is inert |
| Closing | Escape, the close button, or a backdrop click. A backdrop click does nothing when the dialog holds a form with unsaved changes |
| Return focus | To the trigger. If the trigger is gone, to the nearest logical element, never `body` |
| Nesting | Up to 2 dialogs may be open. Escape closes only the top one |
| Live regions | A live region used while a dialog is open sits inside the dialog |
| Content | One task, one column. No side navigation inside |
| Errors | An action started from a dialog keeps the dialog open until it finishes. A failure shows a critical Banner below the header |

The next table gives each Dialog kind on narrow viewports. Primer's Dialog CSS uses `max-width: 767px` for this range.

| Dialog kind | Narrow position | Spec |
| --- | --- | --- |
| Holds one or more input fields | `fullscreen` | `width: 100%; height: 100%; max-width: 100dvw; max-height: 100dvh; border-radius: 0`. Scale and fade in over 200ms |
| No inputs (details, previews, the rail) | `bottom` | `width: 100dvw; max-height: calc(100dvh - 64px)`, top corners 12px, bottom corners 0, anchored to the bottom, slides up from 100% over 250ms. A backdrop tap closes it. In landscape the sheet is at most 480px wide and centred |
| Confirmation | `center` | Keeps its regular width |

#### ConfirmationDialog

| Property | Value |
| --- | --- |
| Base | Dialog with `role="alertdialog"`, width medium (320px) |
| Title | A question under 5 words in sentence case, such as "Delete this page forever?", "Empty trash?", "Delete Design notes?", "Revoke this link?". Never "Are you sure?" |
| Body | The consequence and the item by name, such as "“Roadmap” and its 3 child pages will be deleted. You cannot restore them." |
| Buttons | Cancel (default) on the left, the confirm button on the right. Confirm is danger for destructive actions and names the action in 1 to 3 words, such as "Delete forever", "Empty trash", "Revoke link". Never Yes and No |
| Initial focus | Cancel when the confirm button is danger, otherwise the confirm button |
| Escape | Same as Cancel |
| Result | The dialog stays open with the confirm button busy until the request finishes. Success closes it and moves focus to the next item in the list, or to the list heading when the list is empty. Failure shows a critical Banner under the header |

#### AnchoredOverlay

AnchoredOverlay is the generic floating surface behind `popover()`. It uses the ActionMenu overlay surface and position rules. Width presets are 256, 320, 480, 640 and 960px. It has `role="dialog"` and an `aria-label` that matches its visible heading. Focus moves to the first interactive element on open and returns to the anchor on close. Escape and a click outside close it. On narrow viewports it goes full screen with the close button, as the ActionMenu overlay does.

#### Tooltip

| Property | Value |
| --- | --- |
| Used on | IconButton and icon-only SegmentedControl segments. Never on non-interactive elements, disabled elements or error text |
| Element | A `popover="manual"` element after the trigger, with `aria-hidden="true"`. The trigger references it with `aria-labelledby` (label type) or `aria-describedby` (description type, when the IconButton has a keyboard hint) |
| Surface | bg `--tooltip-bgColor`, text `--tooltip-fgColor`, no border, radius 6px, no shadow |
| Text | `--text-body-shorthand-small` (12px, line height 1.625), centred, `text-wrap: balance`, `overflow-wrap: break-word`. Plain text only |
| Box | Padding 4px 8px, `width: max-content`, max width 250px |
| Position | 4px outside the trigger, below by default (`s`). It moves to stay inside the viewport. An invisible `::after` bridge fills the gap so the pointer can move onto the tooltip |
| Timing | Opens 50ms after hover or immediately on keyboard focus. Fades in over 100ms ease-in under `prefers-reduced-motion: no-preference` |
| Closing | Pointer leaves the trigger and the tooltip, the trigger loses focus, or Escape. Escape does not move focus |
| Keyboard hint | A KeybindingHint inside the tooltip, 6px from the text |
| Touch | Primer states tooltips are never accessible on mobile, so the label must not hold information that the icon and context do not already give |

#### Popover

Markspace has no onboarding or teaching callouts today. A future callout uses Primer's Popover, which has radius 6px, padding 24px and shadow `--shadow-floating-small`. Its 8px caret is drawn in `--borderColor-default` with a 7px `--overlay-bgColor` caret over it. The default width is small (320px). The Popover has a visible dismiss control, receives focus when it opens, and never holds errors or critical information.

### 5.7 Messaging

#### Mapping

| Markspace element | Primer component |
| --- | --- |
| `status()` and `.status-line` | Removed as a message area. Its uses move to the rows below |
| `toast()`, `.toasts`, `.toast` | Removed. Primer does not recommend toasts |
| Sync state (`.sync-pill`, `.ms-status-sync`, `.menu-sync`) | Avatar sync badge and the "Sync now" item of the avatar menu (below) |
| Recovered changes | The "Recovered changes" item of the avatar menu and the warning state of the Avatar badge (below). A new recovered change shows one dismissible Banner warning. No header button, sidebar item or permanent Banner |
| Save failed with Retry | Banner, critical, at the top of the editor, with a Retry action. It stays until the save succeeds |
| "Saved", "Copied", "Created" results | No message when the result is visible. Copy buttons swap `copy` for `check` in `--fgColor-success` for 2 seconds and announce "Copied" through the status region. A settings form that saves without a page change shows an InlineMessage success next to its Save button |
| Undo after trash, remove share, resolve, move, deactivate, remove member | The removed row is replaced in place by an undo row: an InlineMessage (neutral, `--fgColor-muted`) naming the result, and a Button invisible small "Undo". The row stays until the person acts in that list again or leaves the page. No timer. When the place is gone, such as after trashing the open note, a Banner info at the top of the destination page carries the message and the Undo action, and is dismissible |
| Background work the person started and left (import, bulk upload, image generation) | Banner info with a ProgressBar while running, at the top of the page or panel that started it. It is replaced by a success or critical Banner when the work ends, and stays until dismissed |
| Server flash (`partials/flash.html`, `.banner-error`, `.banner-ok`) | Banner critical for errors. Banner success only when the result is not visible on the page |
| Shell banners (`.banners`: offline, signed out, update ready, remote conflict) | Banner warning, or critical when the person must act. One Banner per page |
| Editor banners (`.ms-banner`, `.ms-banner-remote`) | Banner warning inside the editor column, above the note |
| Panel offline note (`.ms-offline-note`) | Banner warning, compact layout, title hidden |
| Panel and row errors (`.ms-error`, `.ms-panel-error`, `.ai-row-status.is-error`, `.usage-error`) | InlineMessage critical beside the failed part, with a Retry Button invisible small where a retry exists |
| Form errors (`.form-error`) | Banner critical at the top of the form, as the error summary of section 6.6 |
| Field errors (`.field-error`) | FormControl validation message, section 5.3 |
| One-time token notice (`.secret-box`) | Banner success with the title "Copy your new token now", the token in a read-only TextInput with a copy IconButton |
| Upload failed (`.upload-row[data-state="failed"]`) | InlineMessage critical, small, in the row |

#### Banner

| Property | Value |
| --- | --- |
| Element | `<section aria-label="{Variant}">` holding an `h2` title (or `h3` to `h6` to fit the outline). Icon `aria-hidden="true"` |
| Layout | `display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: start; padding: 8px`. Compact layout padding 4px |
| Box | 1px border, radius 6px |
| Text | 14px, line height 1.5. Title `font-size: inherit; font-weight: 600; margin: 0`. Content `margin-block: 8px`, 4px between title and body. With the title hidden and no actions, content `margin-block: 6px` |
| Icon cell | Padding 8px, 16px Octicon in a 20px line |
| Dismiss | Padding 8px, 4px start margin, IconButton invisible small `x` in the variant icon colour, named "Dismiss banner". Focus moves to the element after the Banner on dismiss |
| Actions | At most 2, one primary and one default. Column gap 12px, 2px block margin, a 32px minimum row |
| Stacking | Container query at 500px inline size. Narrower containers put actions below the content |
| Flush | No start and end borders, radius 0. Only inside dialogs, tables, cards and boxes |
| Count | At most one Banner on a page. A newer Banner replaces an older one of lower severity |

| Variant | bg | border | Icon colour | Octicon |
| --- | --- | --- | --- | --- |
| critical | `--bgColor-danger-muted` | `--borderColor-danger-muted` | `--fgColor-danger` | `stop` |
| warning | `--bgColor-attention-muted` | `--borderColor-attention-muted` | `--fgColor-attention` | `alert` |
| success | `--bgColor-success-muted` | `--borderColor-success-muted` | `--fgColor-success` | `check-circle` |
| info | `--bgColor-accent-muted` | `--borderColor-accent-muted` | `--fgColor-accent` | `info` |
| upsell | `--bgColor-upsell-muted` | `--borderColor-upsell-muted` | `--fgColor-upsell` | not used |

A dismissible Banner is one the person can ignore. A critical Banner the person must resolve, or one about a destructive action, has no dismiss button. An essential Banner that appears after a page loads, such as an error summary, receives focus. A non-essential one is announced through the status region without moving focus.

#### InlineMessage

| Property | Value |
| --- | --- |
| Layout | `display: grid; grid-template-columns: auto 1fr; column-gap: 8px; align-items: start` |
| Size medium (default) | 14px, line height 20px, 16px outline Octicon |
| Size small | 12px, line height 20px, 12px filled Octicon |
| Icon cell | Min height equal to one line, so the icon centres on the first line |
| critical | Text and icon `--fgColor-danger`, `alert` or `alert-fill` |
| warning | `--fgColor-attention`, `alert` or `alert-fill` |
| success | `--fgColor-success`, `check-circle` or `check-circle-fill` |
| unavailable | `--fgColor-muted`, `alert` or `alert-fill` |
| Placement | Below the input, beside the button, or inside the table row it describes |

#### Avatar Sync Badge

Primer has no sync status component. Markspace shows sync state as a badge on the Avatar button in the app header, with "Sync now" in the avatar menu. The badge shows state and holds no messages. There is no separate sync overlay. Server pages have no badge, because they run no client sync.

| Part | Value |
| --- | --- |
| Badge | A circle at the bottom end corner of the 32px Avatar, with a 2px `--bgColor-default` ring. It is `aria-hidden`. The Avatar button's accessible name carries the state, such as "Account, 3 not synced" |
| Synced | A 16px tick badge: a 10px `check` in `--fgColor-onEmphasis` on `--bgColor-success-emphasis` |
| Syncing and pending | A 12px dot in `--bgColor-attention-emphasis`, without an icon |
| Offline | A 16px warning badge: a 10px `alert` on `--bgColor-neutral-emphasis` |
| Error and signed out | A 16px warning badge: a 10px `alert` on `--bgColor-danger-emphasis`, plus a Banner warning or critical (above) |
| Recovered changes | While a recovered change exists, synced, syncing and pending show a 16px warning badge: a 10px `alert` on `--bgColor-attention-emphasis`. The accessible name adds the count, such as "Account, All changes synced, 2 recovered changes". Offline, error and signed out keep their own badge |
| Avatar menu | The menu starts with the name group and then the sync group. The first item is "Sync now" with a leading `sync` Octicon, and its inline description is the sync status line: "All changes synced", "Syncing 3 changes", "3 not synced", "Offline" or "Signed out". While offline the item uses the inactive style and explains that syncing needs a connection. While a sync runs, the leading visual is a small Spinner. While a recovered change exists, "Recovered changes" follows with a leading `alert` and a CounterLabel, and opens the Recovered changes Dialog. The theme SegmentedControl (2.3) is the row after the sync group |
| New recovered change | One dismissible Banner warning, "Some changes could not be synced and were saved as recovered changes", with the action "Review", which opens the Recovered changes Dialog |

#### Status Region

One visually hidden `<div role="status" aria-live="polite">` sits in `shell.html` and `base.html` from page load. `ui.js` exports `announce(text)` to set its text. Loading completion, filter result counts, copy results and undoable results use it. Form validation does not use it. Section 6.6 moves focus instead.

### 5.8 Labels, Counters, Tokens and Avatars

#### Mapping

| Markspace element | Primer component |
| --- | --- |
| `.pill` with a role, such as "Admin" | Label, accent |
| `.pill-muted`, "Deactivated", access level such as "Can edit", `.readonly-pill` | Label, secondary |
| "Resolved" on comments | Label, done |
| `.pill-danger`, `.ai-chip.is-missing` | Label, danger |
| `.usage-pill-ok`, `.usage-pill-warn` | Label, success and attention |
| `.list-count`, `.nav-link .count`, `.rail-tab-count`, `.icon-badge`, `.upload-indicator .count` | CounterLabel, secondary |
| `.tag` (#tags) | Token, medium, as a link |
| `.ai-chip` (model routes) | Token, medium, static |
| `.ms-chip` (AI context chips) | Token, large, with a remove button |
| `.ms-chip[aria-pressed]` (suggested actions in the ask bar) | Button, default, small. They run an action, so they are buttons |
| `.ms-chip-badge` | Label, secondary, small |
| `.ai-target-rank` | CounterLabel, secondary |
| `.avatar`, `.avatar-sm`, `.avatar-lg`, `.ms-avatar` | Avatar |
| `.avatar-anon` | Avatar with a `person` Octicon |

#### Label

| Property | Value |
| --- | --- |
| Box | `display: inline-flex; align-items: center; border: 1px solid; border-radius: var(--borderRadius-full); background: transparent; white-space: nowrap` |
| Text | 12px, weight 500, line height 1 |
| Size small (default) | Height 20px, padding inline 6px |
| Size large | Height 24px, padding inline 8px |
| default | border `--borderColor-default`, text `--fgColor-default` |
| secondary | border `--borderColor-muted`, text `--fgColor-muted` |
| accent | border `--bgColor-accent-emphasis`, text `--fgColor-accent` |
| success | border `--bgColor-success-emphasis`, text `--fgColor-success` |
| attention | border `--bgColor-attention-emphasis`, text `--fgColor-attention` |
| danger | border `--borderColor-danger-emphasis`, text `--fgColor-danger` |
| done | border `--bgColor-done-emphasis`, text `--fgColor-done` |
| Semantics | `span`. A group of labels is a `ul`. Meaning is in the text, never in colour alone |

StateLabel is Primer's filled status pill for issues and pull requests. Markspace has no such states and does not use it.

#### CounterLabel

| Property | Value |
| --- | --- |
| Box | `display: inline-block; border: 1px solid var(--counter-borderColor); border-radius: 20px; padding: 2px 6px`, 18px tall |
| Text | 12px, weight 600, line height 1, `font-variant-numeric: tabular-nums` |
| secondary (default) | bg `--bgColor-neutral-muted`, text `--fgColor-default` |
| primary | bg `--bgColor-neutral-emphasis`, text `--fgColor-onEmphasis`. Not used |
| Empty | Hidden with `:empty` |
| Screen readers | The visible number is `aria-hidden="true"`, followed by a visually hidden copy in parentheses, such as "(12)". A counter on a tab or icon names what it counts in the control's name, such as "Comments, 3 open" |
| Placement | 8px after the text of a button, tab or nav item. On the rail tab strip the counter sits after the tab label, not over the icon |

A count that cannot be computed, such as while offline, is hidden, not shown as 0.

#### Token

| Size | Height | Padding inline | Font | Remove button |
| --- | --- | --- | --- | --- |
| small | 16px | 4px | 12px | 16px, below the 24px target, not used removable |
| medium | 20px | 6px | 12px | 20px, not used removable |
| large | 24px | 8px | 14px | 24px |
| xlarge | 32px | 12px | 14px | 32px |

| Property | Value |
| --- | --- |
| Box | `display: inline-flex; align-items: center; border-radius: var(--borderRadius-full); border: 1px solid var(--borderColor-muted); background: var(--bgColor-neutral-muted); max-width: 100%` |
| Text | Weight 600, line height 1, `--fgColor-muted`, truncated with an ellipsis |
| Interactive hover | Shadow `--shadow-resting-medium`, text `--fgColor-default` |
| Selected | Border `--borderColor-emphasis`, text `--fgColor-default` |
| Leading visual | 4px end margin, 6px for large visuals |
| Remove button | Round, as tall as the token, 4px start margin (6px for large and xlarge), hover bg `--control-transparent-bgColor-hover`, pressed `--control-transparent-bgColor-active`, `x` icon, named "Remove {label}". With a remove button the token has no end padding |
| Keyboard | A focused removable token is removed with Backspace or Delete |
| Overflow | Hidden tokens collapse into a "+3" token named "3 more" |
| One size per group | Yes |

#### Avatar

| Property | Value |
| --- | --- |
| Sizes | 16, 20 (default), 24, 28, 32, 40, 48, 64px |
| Box | `display: inline-block; border-radius: 50%; overflow: hidden; line-height: 1; vertical-align: middle; box-shadow: 0 0 0 1px var(--avatar-borderColor)` |
| Non-people | Plugins and AI use a square avatar, radius 4px up to 28px and 6px from 30px |
| Image | `alt=""` when the name is shown next to it, otherwise the person's name |
| Markspace sizes | Header account button 32px (20px on narrow is too small, so 32px everywhere). List rows and tables 32px. Comment heads and timeline items 24px. Mentions and chips 16px. Profile header 64px |

Markspace has no profile images, and Primer's Avatar expects an image. The initials form keeps the Avatar box and ring, with the person's stored avatar colour as the background, initials at 40% of the size in weight 600, and `--fgColor-onEmphasis` text. `internal/web` validates that each stored avatar colour gives at least 4.5:1 against white, and darkens it when it does not.

AvatarStack shows 2 to 4 overlapping avatars when space is short. Markspace uses it in the note's PageHeader for the people the note is shared with. The avatars are 20px. The second avatar overlaps the first by 55%, and later avatars overlap by 85%. The third avatar shows at 70% opacity and the fourth at 55%. On hover or focus the avatars spread apart with 4px gaps. A CounterLabel "+N" follows when more than 4 people have access.

### 5.9 Empty, Loading and Progress States

#### Blankslate

| Size | Padding | Heading | Description | Graphic | Markspace use |
| --- | --- | --- | --- | --- | --- |
| small | 16px | 16px, weight 600, line height 1.5 | 14px, line height 1.5 | up to 24px | Rail panels, lists, tables, the file card |
| medium (default) | 32px | 20px, weight 600, line height 1.625 | 16px, line height 1.5 | 24px Octicon | Page content: an empty space, an empty trash, no search results |
| spacious medium | 80px 40px | As medium | As medium | As medium | Error page, offline page, no spaces yet |

| Part | Value |
| --- | --- |
| Layout | `display: grid; justify-items: center; text-align: center`, `text-wrap: balance` |
| Graphic | Octicon in `--fgColor-muted` with 8px below it. `aria-hidden` unless it carries meaning |
| Heading | Margin 0 0 4px. `h2` or `h3` to fit the outline |
| Description | `--fgColor-muted` |
| Primary action | At most one Button primary, 16px above |
| Secondary action | A link below the button, 16px above it |
| Border | 1px `--borderColor-default` with 6px radius when the Blankslate is not the only content |
| Narrow container | A container query at 544px sets padding 20px (16px small, 44px 28px spacious), heading 16px, description 14px, graphic 24px, and 8px above the actions |
| Width | A `narrow` Blankslate is at most 485px wide and centred |

| Markspace element | Blankslate |
| --- | --- |
| `.empty-state`, `emptyState()` | medium |
| `.empty-state.compact`, `.ms-empty`, `.ms-empty.is-compact`, `.list-empty`, `.upload-empty` | small, with border |
| `.switcher-empty`, `.ms-result-empty` | Not a Blankslate. They use the SelectPanel or Autocomplete empty message |
| `.centered` error page, `offline.html`, `.error-code` | spacious medium. The error code appears in the description, such as "Error 404", not as a large number |

#### Spinner

| Part | Value |
| --- | --- |
| Sizes | small 16px, medium 32px, large 64px |
| Drawing | SVG `viewBox="0 0 16 16"`. A track circle `r=7` in `currentColor` at 25% opacity, and the arc `M15 8a7.002 7.002 0 00-7-7`. Both use a 2px stroke with round caps and `vector-effect: non-scaling-stroke` |
| Colour | `currentColor`, at least 3:1 against the background |
| Motion | `rotate(360deg)` over 1000ms linear, repeating. It keeps running under reduced motion |
| Accessibility | SVG `aria-hidden="true"`, a visually hidden "Loading" or a more specific label, and an announcement in the status region when loading ends |
| Delay | Shown only after 1s (6.5) |

| Markspace element | Spinner |
| --- | --- |
| `.boot-spinner` | medium, centred, with "Opening your notes" in 14px `--fgColor-muted` 8px below |
| `.btn[aria-busy]::after` | small, inside the button (5.4) |
| `.cm-ms-ai-hint.is-busy` | small |
| Panel and list loading (`skeletonCards`) | SkeletonBox rows instead (below) |

#### SkeletonBox and SkeletonText

| Part | Value |
| --- | --- |
| SkeletonBox | Block, default height 16px, bg `--skeletonLoader-bgColor`, radius 3px |
| SkeletonText | Height equal to the font size (14px by default), block margin of half the line's leading, radius 3px. A multi-line block ends with a line between 50px and 65% wide |
| Shimmer | Mask `linear-gradient(75deg, #000 30%, rgb(0 0 0 / 0.65) 80%)` at 200% mask size, 1000ms repeating, only under `prefers-reduced-motion: no-preference` |
| Forced colours | 1px transparent outline |
| Announcement | One status announcement for the whole loading region |

`.skeleton-line`, `.ms-skeleton`, `.ms-skeleton-line` and `.ms-skeleton-list` become SkeletonText lines in the shape of the rows they stand for, with a 32px SkeletonBox square for avatars and thumbnails.

#### ProgressBar

| Part | Value |
| --- | --- |
| Track | Flex, `overflow: hidden`, bg `--progressBar-track-bgColor`, radius 3px, `outline: 1px solid var(--progressBar-track-borderColor); outline-offset: -1px`, 2px gap between segments |
| Heights | small 5px, default 8px, large 10px |
| Fill | Width from the percentage. The default colour is `--progressBar-bgColor-accent`, the fuchsia accent. `--progressBar-bgColor-success` fills a bar only when the value means success. Other colours use `--progressBar-bgColor-{attention, danger, done, neutral, severe, sponsors}` |
| Text | Always paired with the value, such as "3.2 MB of 8 MB" or "62%", in 12px `--fgColor-muted` beside or above the bar |
| Markup | `role="progressbar"` with `aria-valuenow` rounded, `aria-valuemin="0"`, `aria-valuemax="100"`, and `aria-valuetext` when the visible text is not a percentage. Not focusable |
| Forced colours | Fill `LinkText`, track `CanvasText` |

| Markspace element | ProgressBar |
| --- | --- |
| `.progress` and `.progress-fill` in upload rows, `.cm-ms-upload-bar` | small, default colour |
| Queued uploads (`.upload-row[data-state="queued"]`) | No bar. A Spinner small with the text "Queued", because Primer's ProgressBar has no indeterminate form |
| `.usage-meter` budget bars | Default height. Default colour below 80%, `--progressBar-bgColor-attention` from 80%, `--progressBar-bgColor-danger` at 100% |
| Plugin `progress` node, image generation, import | Default height, default colour |

#### Timeline

| Part | Value |
| --- | --- |
| Root | Flex column, no list style, `role="list"`, `container-type: inline-size` |
| Item | Flex, padding 16px 0, margin-left 16px, `role="listitem"` |
| Rail | `::before` at left 0, full height, 2px wide, `--borderColor-muted` |
| Badge | 32 × 32px circle, margin-left -15px, margin-right 8px, bg `--timelineBadge-bgColor`, 2px border in `--bgColor-default`, 16px Octicon in `--fgColor-muted` |
| Badge variants | A `--fgColor-onEmphasis` icon on `--bgColor-{accent, success, attention, severe, danger, done}-emphasis` |
| Condensed item | Padding-top 4px, padding-bottom 0 (16px on the last item). The badge is 16px tall with margin 8px 0, no border and bg `--bgColor-default` |
| Body | `min-width: 0`, margin-top 5px, 14px `--fgColor-muted`, `flex: 1 1 auto` |
| Actions | Flex, 8px gap, min height 32px, `margin-left: auto` |
| Break | 24px tall, 4px top border `--borderColor-default`, bg `--bgColor-default`, margin-bottom -16px |
| Narrow container (under 480px) | The item becomes a grid `"badge body" ". actions"`, with the actions below the body and 8px above them |
| Meaning | The event type is stated in text. A badge that carries meaning has an `aria-label` on its Octicon |

#### RelativeTime

Markspace vendors `dist/bundle.js` from `@github/relative-time-element` 5.3.1 (MIT) as `internal/web/static/vendor/relative-time-element-5.3.1.js`. Primer's RelativeTime wraps that element.

| Part | Value |
| --- | --- |
| Markup | `<relative-time datetime="2026-09-13T10:00:00Z">Sep 13, 2026</relative-time>`. The server renders the date as fallback text |
| Formats | Under 1 minute, "just now". Under 1 month, "5 minutes ago", "yesterday", "3 days ago". Older, "on Aug 2". Previous years, "on Aug 2, 2025". No short forms such as "2mo" on any viewport |
| Precise dates | "Thursday, Aug 26, 2026, 3:05 PM" where the exact time is needed, such as version history and token expiry |
| Title tooltip | Turned off with `no-title`. The precise date shows in the Info panel and in version details, because a `title` works only with a mouse |
| Replaces | `timeAgo` in `shell.js`, `relativeTime` in `panels.js`, the Go `timeAgo` output, and `<time title>` in templates |

#### Truncate

| Primer rule | Markspace application |
| --- | --- |
| Prefer wrapping. Truncate only where space forces it | Titles in PageHeaders, Dialog titles and table cells wrap. Tree rows, NavList items, file cards and breadcrumb items truncate |
| Truncation is `overflow: hidden; text-overflow: ellipsis; white-space: nowrap` with a maximum width | As stated |
| Never truncate text that contains links, buttons or mentions | Comment bodies and markdown previews clamp by lines only when they hold no links. Otherwise they show in full or inside Details |
| The full text must be reachable without a `title` tooltip | A truncated tree label is complete in the item's PageHeader. A truncated file name is complete in the file view |

#### Details

| Part | Value |
| --- | --- |
| Markup | Native `<details>` with `<summary>` as the first child. The marker is hidden with `list-style: none` and `::-webkit-details-marker { display: none }` |
| Summary | Button invisible small with a leading `chevron-down` when closed and `chevron-up` when open, and a descriptive label such as "More options" |
| Body | 16px above the first field, fields 16px apart |
| Critical content | Never hidden in Details |

| Markspace element | Details |
| --- | --- |
| `.disclosure`, `.more-options` | As above |
| `.add-toggle` ("New space", "Create account", "Add public link") | The summary is a Button default medium with a leading `plus`. The expanded form ends with Save and Cancel (6.1). The summary keeps its text and does not turn into "Cancel" |
| `.ai-task-details`, `.usage-budget-details`, `.rail-details` | The whole summary row is the toggle, with a trailing `chevron-down` or `chevron-up` |

### 5.10 Editor and File Views

Primer publishes no editor or markdown component. Values below come from Primer components and tokens. Rows that describe Markspace behaviour without a Primer counterpart say so.

#### Editor Bar

| Part | Value |
| --- | --- |
| Element | `.ms-editor-bar`, a plain flex row. It is not a `toolbar`, because it mixes a SegmentedControl, status text and buttons |
| Size | 48px tall, a 32px control row with 8px padding above and below. Inline padding matches the content padding |
| Surface | bg `--bgColor-default`, 1px `--borderColor-muted` bottom border, `position: sticky; top: 0; z-index: var(--zIndex-sticky)` |
| Start | SegmentedControl medium for Live, Source and Reading. The split view segment shows from 1012px |
| Status | Save and sync text in 12px `--fgColor-muted` after a 16px Octicon: `check` when saved, a Spinner small while saving, `cloud-offline` when offline, and `alert` in `--fgColor-attention` when changes exist only on this device. Word count in 12px `--fgColor-muted` from 768px |
| End | IconButton invisible medium for focus mode (`screen-full`, or `screen-normal` while on) and typewriter mode (`typography`), each with a tooltip and `aria-pressed`. Button primary medium "Done" |
| Narrow viewports | The SegmentedControl is icon-only (`pencil` for Live, `code` for Source, `eye` for Reading) with `aria-label`s. The status shows only its icon, with visually hidden text. Done is a Button primary large |
| Below 382px | Find, focus mode and typewriter mode leave the bar. IconButton invisible medium `kebab-horizontal` "More editor actions" opens an ActionMenu with Find (`search`), and Typewriter scrolling and Focus mode as `menuitemcheckbox` items. At 320px the bar holds the SegmentedControl, the status icon, More and Done inside its padding |
| Focus mode | The bar and the app header fade to 0 opacity while the person types, and return on pointer movement or focus, only under `prefers-reduced-motion: no-preference`. With reduced motion they hide and show without a fade. Nothing hides on narrow viewports |

#### SegmentedControl

| Part | Value |
| --- | --- |
| Markup | `<ul aria-label="Editor mode">` of `<li><button aria-current="true">` (or `"false"`). Not a radiogroup, tablist or toolbar. Tab reaches each segment, and Enter or Space selects it |
| Track | `display: inline-flex`, height 32px (28px small), 14px text (12px small), bg `--controlTrack-bgColor-rest`, 1px `--controlTrack-borderColor-rest`, radius 6px |
| Segment | Transparent button, weight 400, `height: 100%`, 4px padding. The inner content has 4px radius, a 1px transparent border and 8px inline padding |
| Selected | Weight 600. Content with 12px inline padding, bg `--controlKnob-bgColor-rest`, 1px `--controlKnob-borderColor-rest`, radius 6px |
| Hover and pressed | Content bg `--controlTrack-bgColor-hover` and `--controlTrack-bgColor-active` |
| Dividers | 1px `--borderColor-default` between segments, 8px from top and bottom, hidden next to the selected segment |
| Icons | 16px `--fgColor-muted`, 4px before a label. Icon-only segments are 32px wide |
| Focus | 2px `--fgColor-accent` outline, offset -1px |
| Width stability | A hidden weight-600 copy of each label reserves its width |
| Hit area | `::before` 44px tall on coarse pointers |
| Options | 2 to 5 text segments, or up to 6 icon-only segments. More choices use an ActionMenu |
| Saving | Applies at once |

| Markspace element | SegmentedControl |
| --- | --- |
| `.ms-seg` editor mode | medium |
| `.files-toolbar .segmented` grid and list | Icon-only, `apps` and `list-unordered`, with tooltips |
| Theme choice in the account menu | Icon-only, `sun`, `moon` and `device-desktop`, with tooltips. The segments are `menuitemradio` items with `aria-checked` (5.5) |
| `.usage-toggle-options` in the usage filter form | Not a SegmentedControl, because the form has an Apply button. RadioGroup |
| Ask bar output mode (Replace, Insert, Answer) | small |
| AI tab scope (This note, This space) | small |

#### Phone Keyboard Bar

Primer has no component for a toolbar above the on-screen keyboard. `ui.md` rule 10 defines it, and its parts use Primer values.

| Part | Value |
| --- | --- |
| Element | `role="toolbar"` with `aria-label` "Formatting" or "Selection". Arrow keys move between buttons behind one tab stop, as in Primer's ButtonGroup toolbar |
| Surface | bg `--bgColor-default`, 1px `--borderColor-default` top border, no shadow |
| Buttons | IconButton invisible large (40px) with 24px Octicons and a 44px hit area, spread evenly up to 56px each: `bold`, `italic`, `heading`, `list-unordered`, `tasklist`, `link`, `sparkle`, `kebab-horizontal` |
| More | ActionMenu, full screen on narrow viewports |
| Review mode | IconButtons `chevron-up` (Previous), `chevron-down` (Next), `check` (Accept) and `x` (Discard), with "Accept all" and "Discard all" in the More ActionMenu |
| Selection mode | IconButtons `bold`, `italic`, `link`, `code`, `comment` while online, `sparkle` with edit access, and `kebab-horizontal` "More selection actions". Its ActionMenu holds Strikethrough and "Find and replace". Find starts with the selected text |
| Find | The More formatting ActionMenu holds "Find and replace" while the editor bar has no Find button, below 382px |
| Position | Section 8 |

#### Find Bar

`find.js` draws one find bar for Live, Source and Reading. Primer has no find component. The parts use Primer components and tokens.

| Part | Value |
| --- | --- |
| Element | `.ms-find`, `role="search"` named "Find in note". Hidden until opened |
| Main row | IconButton invisible small `chevron-right` "Toggle replace" with `aria-expanded`, TextInput small "Find" with a leading `search`, the match count, a `group` "Search options" of IconButton small toggles with `aria-pressed` (Match case `Aa`, Whole word `ab`, Regular expression `.*` in 12px weight 600 monospace), IconButton invisible small `chevron-up` "Previous match" and `chevron-down` "Next match", and IconButton invisible small `x` "Close find" at the end |
| Replace row | TextInput small "Replace" in line with the Find field, Button small "Replace" and Button small "Replace all". It shows while the disclosure is open |
| Match count | 12px `--fgColor-muted` tabular numerals, `aria-live="polite"`: "3 of 12", "12 matches" without a current match, "No results", or past 1000 matches "1000+". An invalid regular expression shows an InlineMessage critical small and sets `aria-invalid` on Find. Previous, Next, Replace and Replace all take `aria-disabled` without matches |
| Short count | Below 382px the count cell is 64px wide. The count shows "3/12", "12" or "0", with the full text as its tooltip. The full text stays as visually hidden text for screen readers |
| Keys | Ctrl+F (⌘F) opens Find and selects its text. Ctrl+H, or ⌘⌥F on macOS, opens Replace. Enter and Shift+Enter in Find, F3 and Shift+F3, and Ctrl+G and Shift+Ctrl+G step through matches. Enter in Replace replaces the current match, and Ctrl+Enter replaces all. Alt+C, Alt+W and Alt+R flip the options. Escape closes the bar and returns focus to the note at the current match |
| Entry points | IconButton invisible medium `search` "Find" in the editor bar, with `aria-expanded` and `aria-controls`. Below 382px Find opens from the editor bar's More editor actions menu, the keyboard bar's More formatting menu, and the selection tools' More menu. The note view's PageHeader More actions menu on narrow viewports has "Find in note" with `search`. It opens the editor's find bar through the editor handle when an editor is mounted, and otherwise a Reading find bar above the rendered note |
| Prefill | Selected text of at most 100 characters on one line fills Find when the bar opens. With Regular expression on, the text is escaped |
| Placement | `position: sticky` under the editor bar, `top: var(--ms-bar-h)`, bg `--bgColor-default`, 1px `--borderColor-muted` bottom border, 8px block padding and the editor bar's inline padding. It takes its own space in the page flow, and the scroll padding grows by its height. In the note view it sticks at `top: 0` inside the note's padding |
| Phones | With the on-screen keyboard open, or on a phone viewport under 320px tall, the bar is fixed above the keyboard bar with a 1px `--borderColor-default` top border. From 768px the fixed bar keeps one row. Android shrinks the layout viewport for the keyboard, because the viewport meta sets `interactive-widget=resizes-content`, so the viewport height alone sets the bar position. Below 768px it is a grid: Find and Close on the first row, the count, the options, Previous and Next on the second, and the Source link on a third. Buttons are 44px and the fields use 16px text |
| Live and Source | Search the markdown source through the CodeMirror search state, so syntax that Live hides is still found. Matches use `--highlight-neutral-bgColor`. The current match mixes 40% `--bgColor-accent-emphasis` with a 2px `--borderColor-accent-emphasis` underline, in every mode. Replace and Replace all change the source, and Replace all is one undo step |
| Reading | Marks matches in the rendered text with `mark` elements. A match never crosses a block boundary, and the title is skipped. Replace is hidden. With edit access a Button link small "Switch to Source to replace" takes its place and switches to Source with Replace open |
| Shared query | One query and one set of options serve all three modes. A mode switch keeps the query and maps the current match to the same position in the new mode's list of matches |
| Without edit access | No Replace disclosure and no Source link |

#### Selection Toolbar (Desktop)

| Part | Value |
| --- | --- |
| Surface | AnchoredOverlay surface with bg `--overlay-bgColor`, radius 12px, shadow `--shadow-floating-small`, padding 4px, 4px from the selection |
| Content | `role="toolbar"` of Button invisible small and IconButton invisible small, with arrow-key navigation |
| Groups | Separated by a 1 × 20px `--borderColor-muted` divider with 4px side margins |
| Phones | Not shown. The keyboard bar switches to selection mode |

#### Slash Menu and Link Completion

| Part | Value |
| --- | --- |
| Component | Autocomplete list anchored to the caret (`.cm-tooltip-autocomplete`) |
| Surface | The ActionMenu overlay surface, 320px wide (max `calc(100vw - 32px)`), max height `min(320px, 100dvh)`, 4px from the caret line. It opens above the caret when less than 320px of the visual viewport remains below it |
| List | ActionList `role="listbox"` with the inset list padding. CodeMirror sets `aria-activedescendant` on the editor |
| Items | 32px (40px on narrow viewports). A leading 16px Octicon (`heading`, `list-unordered`, `list-ordered`, `tasklist`, `quote`, `code`, `table`, `horizontal-rule`, `link`, `image`, `file`, `sparkle`), a 14px label, and the detail (`.cm-completionDetail`) as an inline description in 12px `--fgColor-muted` |
| Active item | bg `--control-transparent-bgColor-selected` with the 4px `--borderColor-accent-emphasis` bar |
| Matched text | Weight 600, no underline |
| Sections | ActionList group headings in 12px weight 600 `--fgColor-muted`, sentence case |
| Empty | "No matches" in 14px `--fgColor-muted` with 16px padding |
| Narrow viewports | Stays anchored and does not go full screen, so the typed text stays in view |

#### Notes in Live Preview and Reading View

The rules below apply Primer tokens to markdown.

| Element | Value |
| --- | --- |
| Body | `--text-body-shorthand-large` (16px, line height 1.5), `--fgColor-default`, max width `--ms-measure` |
| Title field (`.ms-title`) | PageHeader title type, 32px weight 400 with line height 1.5, or 20px weight 600 on narrow viewports. No border. On focus, a 2px `--borderColor-accent-emphasis` bottom border replaces the outline, so the indicator is replaced and not removed |
| Headings | The markdown scale in 3.2 |
| Links and wikilinks | `--fgColor-link`, underlined with `text-underline-offset: 0.2em`. A wikilink to a missing note is `--fgColor-muted` with a dashed underline |
| Inline code | `--fontStack-monospace` at 85%, bg `--bgColor-neutral-muted`, padding 0.2em 0.4em, radius 6px, `white-space: break-spaces`, as in Primer CSS `markdown.css` |
| Code block | `--text-codeBlock-shorthand`, bg `--bgColor-muted`, padding 16px, radius 6px. The language label is 12px `--fgColor-muted`. Copy is IconButton invisible small `copy` |
| Quote | 0.25em start border `--borderColor-default`, padding 0 1em, `--fgColor-muted` |
| Callouts | Primer CSS has no alert rules, so callouts reuse the quote shape: a 0.25em start border and a title row in weight 600 with a 16px Octicon. Note uses `info` with `--borderColor-accent-emphasis` and `--fgColor-accent`. Tip uses `light-bulb` with the success tokens, important `report` with done, warning `alert` with attention, and caution `stop` with danger. No background fill |
| Horizontal rule | 0.25em tall in `--borderColor-default`, 24px above and below |
| Tables | `display: block; width: max-content; max-width: 100%; overflow: auto`, tabular numerals, cell padding 6px 13px, 1px `--borderColor-default` borders, header weight 600, even rows bg `--bgColor-muted`. The h6 heading is `--fgColor-muted` |
| Task checkbox | Checkbox (5.3) |
| Comment anchors | bg `--bgColor-accent-muted`. The active anchor has bg `--borderColor-accent-muted` with `box-shadow: inset 0 -2px 0 var(--borderColor-accent-emphasis)` |
| Search matches | bg `--highlight-neutral-bgColor` |
| Selection | `--selection-bgColor` |
| Caret | 2px `--codeMirror-cursor-fgColor` |
| Image action button | IconButton default small `kebab-horizontal`, 8px from the image's top end corner |
| Upload widget | A Box row (5.2) with a 40px thumbnail at 6px radius, the name in 14px weight 600, meta in 12px `--fgColor-muted`, a ProgressBar small, an InlineMessage small on failure, and IconButton invisible small actions |

#### Ask Bar

| Part | Value |
| --- | --- |
| Element | A CodeMirror block widget in the note flow, as `assistant.md` requires |
| Surface | bg `--bgColor-default`, 1px `--borderColor-default`, radius 6px, shadow `--shadow-resting-small`, padding 8px, 8px between rows |
| Input | A Textarea without its own border inside the box, 14px, growing to 6 lines, with a visually hidden label "Ask AI" and a leading `sparkle` visual |
| Context chips | Tokens large with remove buttons, followed by the size estimate in 12px `--fgColor-muted`, which turns `--fgColor-attention` past 75% |
| Suggested actions | Button default small, wrapping with 8px gaps |
| Second-row choices (tone, language) | Button default small |
| Output mode | SegmentedControl small |
| Model chip | Button invisible small with a trailing `triangle-down`, opening a SelectPanel |
| Send | Button primary small "Ask". While streaming, Button default small "Stop" |
| Answer card | A box with the ask bar surface, markdown at 14px, citation Tokens medium as links, and Buttons small "Insert below", "Replace selection", "Copy" and "Continue in AI tab" |
| Offline | Every trigger uses the inactive style, and the bar shows an InlineMessage warning "AI needs a connection" |
| Mentions list | Autocomplete list (5.3) under the input |

#### Tracked Changes and Suggestions

| Part | Value |
| --- | --- |
| Removed text | bg `--diffBlob-deletionWord-bgColor`, `text-decoration: line-through`, `--fgColor-default` |
| Inserted text | bg `--diffBlob-additionWord-bgColor`, `--fgColor-default` |
| Header above the first hunk | The author and title, such as "AI · Improve", in 12px weight 600 `--fgColor-muted`, then Button primary small "Accept all", Button default small "Discard all", and for live results Button invisible small "Save as suggestion" |
| Hunk controls (desktop) | IconButton invisible small `check` ("Accept change") and `x` ("Discard change") in the start margin of the hunk's first line, with tooltips showing their keyboard hints |
| Phones | Keyboard bar review mode (above) |
| Colours | Primer's diff tokens, as section 10 row 25 states |

#### Comments Panel

| Part | Value |
| --- | --- |
| Thread | No Box. The quoted passage and a condensed Timeline of its comments. A 1px `--borderColor-muted` divider sits in the middle of the 24px gap between threads. The reply Textarea is the only bordered element |
| Quoted passage | Plain text in 12px `--fgColor-muted` with an 18px line height, clamped to 2 lines, with no border and no background. A passage still in the note is a button whose name is the full text plus "Show in note". It has a `--control-transparent-bgColor-hover` background on hover, an inset focus ring, and a 44px minimum height on touch screens and below 768px. A passage removed from the note is italic and not a button, with the caption "Removed from the note", or "Text no longer in the note" on a resolved thread |
| Comment item | A condensed Timeline item with a 24px Avatar in the badge position. Author in 14px weight 600 `--fgColor-default`, RelativeTime in 12px `--fgColor-muted`, "edited" in 12px `--fgColor-muted`. Body in 14px `--fgColor-default` with `white-space: pre-wrap`. Actions in an IconButton invisible small `kebab-horizontal` with Edit and Delete |
| Resolve | "Resolve thread" in the first comment's overflow menu. It acts at once and replaces the thread with an undo row. A resolved thread shows a Timeline event "Resolved by Ana, 2 hours ago", with the time from `updatedAt`, and Button default small "Reopen" with `issue-reopened` in its footer. Reopen acts at once with a short announcement. A resolved thread never shows Undo |
| Reply | A Textarea of 3 rows with Button primary small "Reply" at the end and Button default small "Cancel" before it, shown when the thread has focus or holds draft text |
| Groups | Open threads first, then a Details "Resolved (4)". Resolved threads keep their quoted passage |
| Narrow containers | Timeline actions wrap under the body below 480px |
| New comment | The selection toolbar or keyboard bar opens a composer Box at the top of the panel |
| Empty | Blankslate small with `comment`, the title "No comments yet" and the description "Select text in the note and choose Comment" |

#### Version History

Markspace shows version history as a full-screen Dialog with a diff view. Primer has no version history component.

| Part | Value |
| --- | --- |
| Versions | Each version is a Timeline point grouped under its day, with its time, author, "via" and the plugin when a plugin wrote it, and the size change. Current comes first |
| Entry points | The History rail tab shows Current and the latest 4 versions, then Button small "Show diff" with a leading `history` and a CounterLabel of the version count. A row in the rail opens the Dialog on that version. The note's PageHeader More menu has "Version history" with `history`, which opens the Dialog with the latest version selected. Without earlier versions the menu item shows a Banner info "No earlier versions" |
| Dialog | Full screen on every viewport, with the title "Version history", the note title as subtitle, and a close button. Escape closes it and focus returns to the trigger |
| Layout | From 768px the version list sits on the start side and the diff on the end side. Below 768px the list and the diff are two screens. Choosing a version shows its diff, and Button small "Back" with `arrow-left` returns to the list |
| List | A `listbox` named "Versions" with the day groups above. It loads 20 versions at a time, with Button small "Show older versions". Up, Down, Home and End move the selection |
| Diff view | The header shows the date, time, author, "via" and the line count of the selected version. A SegmentedControl small switches between "Changes in this version", the default, against the version before it, and "Compare with current". The diff is a read-only CodeMirror merge view from `@codemirror/merge` 6.12.2. It is side by side from 768px and unified below, with unchanged regions collapsed. It scrolls on its own |
| Footer | Button "Download" and Button primary "Restore this version", which act on the selected version. Restore first saves the current text as a new version, so it is undoable and needs no confirmation. Its Undo Banner shows in the rail panel when the panel is visible, and at the top of the page otherwise |

#### Share Panel

| Part | Value |
| --- | --- |
| People | Section heading "People" with a CounterLabel, then Box rows. Each row has an Avatar 32px, the name in 14px weight 600, the email in 12px `--fgColor-muted`, an access ActionMenu single-select trigger (Button small "Can edit" with `triangle-down`), and an IconButton invisible small `kebab-horizontal` whose menu ends with "Remove access" as a danger item |
| Add a person | Autocomplete "Email or name", Select "Access", Details "More options" holding Checkbox "Include child pages", and Button primary "Share" |
| Public links | Box rows, each with a read-only monospace TextInput with a copy trailing action, a Label secondary for the access level, a Label "Password" when one is set, the view count in 12px `--fgColor-muted`, and an IconButton invisible small menu with Copy link and Revoke link (danger, ConfirmationDialog) |
| New public link | A Details summary Button default "Add public link", expanding to Select "Access", an optional password TextInput with a caption and no placeholder, Checkbox "Include child pages", and Button primary "Create link" |

#### Files Panel and File Cards

| Part | Value |
| --- | --- |
| Drop area | 5.3 |
| Upload list | Box rows, as in the upload widget |
| File list | Box rows with a 40px thumbnail or file type Octicon, the name, size and RelativeTime, and an IconButton menu |
| File grid (`.file-card`) | Box with radius 6px, a 4:3 thumbnail on `--bgColor-muted`, 12px padding, the name in 14px weight 600 truncated, and meta in 12px `--fgColor-muted`. The action button is an IconButton default small `kebab-horizontal`, 8px from the top end corner, always visible |

#### File View

| Part | Value |
| --- | --- |
| Header | PageHeader with the file type Octicon as leading visual, a medium title with the file name, and a description with size, type and RelativeTime. Actions are Button primary "Download" with a leading `download`, and IconButton default medium `kebab-horizontal` with Open in new tab, Rename, Move to, Copy to, Replace, Keep available offline, Copy link, Share, and Move to trash last |
| Narrow viewports | A parent link to the folder. Download becomes IconButton default large, and the menu IconButton invisible large |
| Viewer toolbar (`.fv-toolbar`) | `role="toolbar"` flex row with 8px gap, a 1px `--borderColor-muted` bottom border and 8px block padding. Tools are IconButton invisible medium with tooltips. The zoom level is 12px `--fgColor-muted` in tabular numerals. Fit and 100% form a SegmentedControl small |
| PDF page field | TextInput small, 4ch wide, labelled "Page", followed by "of 12" in 12px `--fgColor-muted` |
| Find in document | TextInput small with a leading `search`, the match count in 12px `--fgColor-muted`, and IconButton invisible small `chevron-up` and `chevron-down` |
| PDF thumbnails | PageLayout pane at the start, width small (240px from 768px, 256px from 1012px), toggled by IconButton invisible medium `sidebar-expand`. Hidden on narrow viewports |
| Image stage | bg `--bgColor-inset` |
| CSV | DataTable (5.2) |
| Text and code | Code block values in a read-only CodeMirror |
| Other files (`.fv-other`) | Blankslate medium with the file type Octicon at 24px, a description list of size, type and dates, and Button primary "Download" |
| Error (`.fv-error`) | Blankslate medium with `alert` in `--fgColor-muted`, the title "This file could not be shown", and Button default "Download" |
| Loading (`.fv-loading`) | Spinner medium, centred, after 1s |

### 5.11 Plugin Declarative UI

The renderer maps each node type in `plugins.md` to one Primer component. It escapes all text and never renders plugin-supplied HTML.

| Node | Primer component and spec |
| --- | --- |
| `stack` | Flex column. `gap` `s` is 8px (`--stack-gap-condensed`), `m` 16px (`--stack-gap-normal`), `l` 24px (`--stack-gap-spacious`) |
| `section` | Section heading (5.2): `h2` at title small, `count` as a CounterLabel, `description` in 14px `--fgColor-muted` |
| `form` | A form with `novalidate`. `title` and `description` form a section heading. Fields are FormControls 16px apart. `more` fields sit inside Details "More options". `submit` is a Button primary at the bottom start. Validation follows 6.6 |
| field `text`, `url`, `number` | TextInput of that type |
| field `textarea` | Textarea |
| field `select` | Select, or a RadioGroup when it has 6 options or fewer |
| field `checkbox` | Checkbox in a horizontal FormControl |
| field `file` | Button default "Choose file" that opens a hidden file input, with the chosen name as the caption |
| field `note`, `space` | Autocomplete over the notes or spaces the actor can view |
| field `folder` | SelectPanel single select listing folders by path |
| field `hint`, `required` | Caption and required marker (5.3) |
| `list` | Box rows. `title` in 14px weight 600, as a link when `link` is set. `subtitle` in 12px `--fgColor-muted`. `icon` is a 16px Octicon from the vendored set and is ignored when unknown. `badge` is a Label. One action is a Button small, and two or more go in an ActionMenu. `empty` is a Blankslate small |
| `table` | DataTable. `numeric` columns are end aligned in tabular numerals. `rowActions` sit in an ActionMenu named "Actions: {first cell}" |
| `text` | Paragraph in 14px `--fgColor-default` |
| `markdown` | Note renderer output at `--text-body-shorthand-medium`, with raw HTML escaped |
| `progress` | ProgressBar at default height with the `label` and "value of max" text. `jobId` updates `aria-valuenow` |
| `badge` | Label. `neutral` maps to secondary, `good` to success, `warn` to attention, `bad` to danger, `accent` to accent |
| `button` | Button. `primary`, `secondary` (Primer's default) or `danger`. `confirm` opens a ConfirmationDialog whose title is the `confirm` text and whose confirm button repeats the button label |
| `link` | A link in `--fgColor-link`, underlined. `http:` and `https:` URLs open in the same tab |
| `empty` | Blankslate small, with `action` as a Button primary |
| `code` | Code block (5.10) in a region with `tabindex="0"` and `aria-label="Code"` |
| Command response `toast` | InlineMessage success beside the button that ran the command. When that button is gone, a Banner success at the top of the plugin panel |

## 6. Patterns

### 6.1 Saving

| Primer rule | Markspace application |
| --- | --- |
| Start with explicit saving. Never mix save patterns in one form, and avoid mixing them on one page | Settings, space settings, profile, tokens, AI routes and admin forms have one Save button per form |
| Text inputs, checkbox groups, radio groups and native selects are saved by a submit button | Every such field waits for Save. The current save-on-change of settings fields ends |
| ToggleSwitch, SegmentedControl and custom single-select menus save as soon as they change, with no Save button and no "Saved" indicator | Feature switches, the editor mode switch, grid and list views, and row role changers save at once |
| Never disable or hide a Save button, even when the form is invalid or unchanged | Save is always enabled. Submitting an unchanged form does nothing and shows nothing |
| One Save button per page for a single form. Page forms place it bottom left with Cancel to its right. Dialogs and comment boxes place it bottom right with Cancel to its left | As stated |
| Inline editing places Save beside the input | Rename in the tree, lists and headers shows the name in a small TextInput with Button small primary "Save" and Button small invisible "Cancel" beside it. Enter saves and Escape cancels as well |
| Feedback only when the change is not visible. No page change means InlineMessage beside Save. A page change means a Banner | A profile save shows InlineMessage success "Profile saved" beside Save. Creating a space navigates to it and shows nothing |
| On a save error keep the entered data | Forms re-render with the submitted values and the error summary |
| Warn about unsaved changes with `beforeunload` | Every settings and admin form that edits values has `data-unsaved`: profile, password, token name, space settings, admin settings, AI routes and model prices, usage defaults and budgets, and plugin settings and secrets. `app.js` marks such a form changed on `input` and `change` while its values differ from the values before the first edit, and a submit clears the mark. While a form is changed, the page sets `beforeunload`, and a link to another page opens a ConfirmationDialog "Discard unsaved changes?" with "Discard changes" and "Keep editing". A form that saves through a script sends `markspace:saved` to take its new values as the baseline |
| Note text | Primer does not cover documents that save continuously. The editor keeps continuous saving. Sync state is shown by the avatar sync badge (5.7), and a failure by a critical Banner |

### 6.2 Destructive Actions

| Primer rule | Markspace application |
| --- | --- |
| Destructive or irreversible actions show a ConfirmationDialog | Delete forever, Empty trash, Delete space, Delete account, Delete user, Uninstall plugin, Revoke token, Revoke public link, Delete comment, Discard unsaved changes, and a plugin `button` with `confirm` |
| A non-destructive action offers Undo instead of a confirmation. "Never use a warning when you mean undo" | Move to trash, Remove access, Resolve thread, Move, Deactivate user, Remove member and Remove from space act at once and offer Undo (5.7) |
| Danger items go last in a menu, after a divider | Move to trash and Delete are the last items in every menu |
| Do not confirm frequent or easily undone actions | No confirmation for trash, resolve or move |
| Avoid a keyboard shortcut for an action that is destructive without confirmation | Delete in the tree moves to trash, which is undoable. Shift+Delete, if added, opens the ConfirmationDialog |
| After a delete, focus moves to the next logical item, never `body` | Focus goes to the next row's primary element, then the previous row, then the list heading |

### 6.3 Progressive Disclosure

| Primer rule | Markspace application |
| --- | --- |
| Use disclosure sparingly, only to truncate information | "More options" in forms, row details, resolved comments and version details |
| A chevron toggles collapsed content. It points down when collapsed and up when expanded | `.disclosure`, `.ai-task-chevron`, `.ms-card-chevron` and `.usage-budget-summary` use `chevron-down` and `chevron-up`. Tree toggles use TreeView's chevron (5.1) |
| A kebab opens menus | Every row menu uses `kebab-horizontal` |
| A caret (`triangle-down`) marks a dropdown trigger, never disclosure | Buttons that open ActionMenu or SelectPanel have a trailing `triangle-down` |
| An ellipsis toggles truncated inline text | Long snippets in search results use an ellipsis Button invisible small |
| Avoid text-only toggles. Pair the icon with text | "More options" keeps its chevron and label |
| Disclosure markup | `details` and `summary` (Details, 5.9) |

### 6.4 Notifications

| Primer rule | Markspace application |
| --- | --- |
| Toasts are not recommended | No toasts |
| Place messages near the action. Page or section messages go at the top of the body content | 5.7 mapping |
| A simple success that is visible needs no message, but screen readers still get the state | Status region announcements only |
| Failures use a Banner (passive) or a Dialog (interrupting). Crucial user-initiated failures use a simple error Dialog | Save and sync failures use a Banner. A failed drag and drop move uses a small Dialog explaining why, closed by its button or a click outside |
| Long-running tasks report through a Banner | Import and bulk upload, 5.7 |
| Application out of sync uses a Dialog or Banner asking for a refresh | "A newer version of Markspace is available" is a Banner warning with a "Reload" action |
| Never auto-dismiss feedback | No timers on any message |
| Always announce location changes and failed actions | Soft navigation moves focus to the page `h1` (with `tabindex="-1"`) and updates `document.title` |

### 6.5 Loading

| Wait | Primer rule | Markspace application |
| --- | --- | --- |
| Under 1s | No loading state | Every loading indicator starts after a 1s delay. The current 400ms and 500ms delays become 1s |
| 1s to 3s | Indeterminate indicator | Spinner for small areas and buttons. SkeletonBox rows for lists, tables and panels |
| 3s to 10s | Determinate indicator where possible | ProgressBar for uploads, imports and image generation |
| Over 10s | Determinate indicator, and the work runs in the background without blocking | Uploads and imports continue while the person navigates. The Banner in 5.7 reports the end |

| Rule | Markspace application |
| --- | --- |
| Put the indicator where the content will appear, centred in large areas, and avoid layout shift | The boot screen centres a Spinner medium. Skeleton rows match row height |
| One indicator for a group, not one per item | One Spinner per panel, not per card |
| Label indicators, such as "Loading comments" | Spinner has a visually hidden label |
| Announce the end with `role="status"` when it is not obvious, and set `aria-busy` on the region while it updates | `aria-busy="true"` on the list while loading |
| Focus the first new item after "Load more" | Applies to search results, history and the trash |

### 6.6 Forms and Validation

| Primer rule | Markspace application |
| --- | --- |
| Labels are visible, at most 3 words, sentence case, without colons. A placeholder is never a label | Every field has a visible `label`. Placeholders are removed where they repeat or replace a label. Search fields keep a visible label or an `aria-label` with a leading `search` icon, as Primer's TextInput allows for search |
| Mark required fields visibly and with `required`, except where every field is required, such as sign in | A "Required" caption style from FormControl (5.3) |
| A caption adds new information and sits under the input | `.field-hint` becomes the FormControl caption |
| Stack fields vertically. Do not lay out columns to save space | `.field-row` side-by-side fields stack, except closely related short pairs such as a space name and its icon |
| Use Primer validation, not browser validation messages | Every form has `novalidate`. The server and `ui.js` render the messages |
| Validate on submit | As stated |
| After a failed submit with 1 or 2 errors, focus the first invalid field and scroll it into view | As stated |
| With 3 or more errors, show an error summary Banner at the top of the form, listing each error as a link that focuses its field, and move focus to the Banner | `.form-error` becomes this Banner |
| Mark each invalid field with `aria-invalid="true"` and link its message with `aria-describedby`. Do not use live regions | As stated |
| After the first failed submit, a field may validate inline after it changes and loses focus, and clears its error while typing | The rename field and the new space form validate inline after the first error |
| A validation that takes more than 1s shows a loading indicator | Slug availability checks show a Spinner small in the input |
| Error text is specific, does not blame and does not apologise | "Enter a name", "The address can use lowercase letters, digits and dashes" |
| Submit buttons are never disabled | As stated |

### 6.7 Navigation

| Primer rule | Markspace application |
| --- | --- |
| NavList items change the URL | The sidebar's Starred, Recent, Shared, spaces and footer links, the settings navigation and the admin navigation |
| TreeView is not navigation. It selects an item in a hierarchy and may change the URL | The page tree |
| UnderlineNav tabs change the URL. UnderlinePanels tabs switch content without changing it. The two are never mixed | Settings and admin top tabs, if kept, are UnderlineNav. Rail tabs are UnderlinePanels |
| Tabs sit directly above the content they change | As stated |
| A child page offers a way back to its parent through breadcrumbs or a back link | Note pages keep breadcrumbs. Settings sub-pages on narrow viewports show a parent link in the PageHeader |
| Skip links pass the global header and dense content | "Skip to content" stays. A second skip link passes the page tree |
| After a soft navigation, focus the page heading and update the title | As stated in 6.4 |

### 6.8 Empty States

| Primer rule | Markspace application |
| --- | --- |
| Use Blankslate with a graphic, a title, an optional description, one primary action and an optional secondary link | `emptyState()` in `panels.js`, `.empty-state` and `.list-empty` (5.9) |
| A first-use empty state welcomes and explains | "Start your first page", with a "New page" primary action |
| A temporary empty state says the list is empty because of what it holds | "The trash is empty", "No comments yet" |
| An error state uses the `alert` icon in `--fgColor-muted`, a specific summary and a way forward | "Comments could not be loaded", with a "Try again" Button |
| Do not show an empty state for data that is unavailable | Offline panels show "Comments need a connection" in a Banner warning, not "No comments yet" |
| Add a border when the Blankslate is not the only content | Blankslates inside panels and lists have a border |
| A table with no data is replaced by a Blankslate | Admin and settings tables |

### 6.9 Degraded Experiences

| Primer rule | Markspace application |
| --- | --- |
| Do not disable a control because a service is unavailable. Remove it, or use the inactive style when it is critical | AI triggers while offline use the inactive style. A tap opens an AnchoredOverlay saying "AI needs a connection". Share and history actions that need the server are removed while offline |
| Hide counts that cannot be computed | Rail and nav counters are hidden while their data is unknown |
| Limit a page to 5 outage messages | The offline Banner replaces per-panel offline notes when 3 or more panels are affected |
| Never hide the global header | The app header stays in every state, including focus mode on regular viewports, where it scrolls away with the page |

### 6.10 Keyboard Shortcuts

| Primer rule | Markspace application |
| --- | --- |
| A shortcut is never the only way to act | Every shortcut has a button or menu item |
| Single-key shortcuts work only while their component has focus, or can be turned off | F2, Delete and letter type-ahead work only while the tree has focus |
| Avoid Ctrl or Cmd with W, T, N, F and L | Focus mode moves from `Ctrl Shift F` to `Ctrl Shift .`. No new shortcut uses those letters |
| `?` opens a dialog listing every shortcut | Markspace has no shortcut dialog. Menu items and IconButton tooltips show each shortcut |
| Show shortcuts in `kbd` and set `aria-keyshortcuts` | Menu items and IconButton tooltips show a KeybindingHint. Triggers carry `aria-keyshortcuts`, such as `Control+K Meta+K` |

KeybindingHint is `kbd` text at 12px in `--fgColor-muted` inside a 1px `--borderColor-default` box with 3px radius and 0 4px padding. Chords are written with the platform's modifier names.

### 6.11 Data Tables

| Primer rule | Markspace application |
| --- | --- |
| Use a table to compare many values in a flat set. Use a list when columns are mostly empty or items need hierarchy | Users, members, tokens, providers, models, usage by model and recent calls stay tables. Trash, shares and links are lists |
| The first cell is a row header and visually distinct | `th scope="row"` with the name in weight 600 |
| Right-align numbers with tabular numerals | `.col-num` |
| Leave empty cells blank. No dashes or "Never" placeholders unless the text explains | "Last sign in" shows "Never signed in" only as explanation text |
| Loading uses skeleton rows | 5.9 |
| Paginate from 20 rows | Admin users and recent calls page at 20 |
| An empty table is replaced by a Blankslate | 6.8 |
| On narrow screens, hide less important columns or scroll sideways | Section 7 |

## 7. Narrow Viewports

Primer's narrow range is below 768px. Markspace uses 390 × 844 as its reference phone and checks reflow at 320 × 256.

### 7.1 Primer Rules

| Area | Primer rule |
| --- | --- |
| Columns | One column below 768px |
| App header | Never fixed. It scrolls with the page |
| Scrolling | The page scrolls as a whole. Panes are not sticky below 768px |
| Sidebar of links | Becomes an index page, reached through a parent link or breadcrumbs. A left side sheet is reserved for global navigation drawers |
| Pane of details | Stacks under the content or opens as a bottom sheet |
| ActionMenu, SelectPanel, AnchoredOverlay | Full screen at `100vw × 100dvh` with radius 0, a close button 8px from the top end corner, and bottom padding `env(safe-area-inset-bottom)`. SelectPanel shows Cancel and Save in a footer |
| Dialog | Bottom sheet or full screen. Any Dialog with an input field is full screen. A bottom sheet in landscape is at most 480px wide and centred |
| ConfirmationDialog | Centred at its regular width |
| Tooltip | Not available on touch. Information in a tooltip must also be available elsewhere |
| UnderlineNav | Icons hide in containers under 768px. Items that do not fit move into a More menu |
| UnderlinePanels | The strip scrolls sideways inside itself |
| SegmentedControl | Icon-only segments or an ActionMenu. It never wraps or drops segments |
| PageHeader | The context area with the parent link shows. Leading and trailing actions hide, and other actions move into an overflow menu |
| Breadcrumbs | Replaced by the parent link |
| Banner | Actions stack below the message |
| Blankslate | Smaller padding and type through its container query |
| Timeline | Actions wrap below the body under 480px |
| DataTable | Hide less important columns, or scroll sideways in a named, focusable region |
| Pagination | Page numbers stay |
| Targets | 24px minimum, 44px recommended. Buttons use the large 40px size |
| Reflow | Usable at 320 × 256 and at 200% zoom without horizontal page scrolling |
| Zoom and orientation | `user-scalable=no` is never set. Portrait and landscape both work |

### 7.2 Markspace at 390px

| Screen part | Result |
| --- | --- |
| App header | 64px, scrolls away. IconButton large `three-bars`, the space name truncated, IconButton large `search`, IconButton primary large `plus` when the PageHeader has no New page action, and the Avatar 32px in a 44px button with the sync badge when not synced |
| Sidebar | Left Dialog side sheet 320px wide, holding the space switcher, NavLists, the page tree and footer links. Tree rows are 44px |
| Note page | PageHeader with the parent link, a medium title, "Edit" as Button primary large, IconButtons large for Comments and Details, and an overflow menu. Note text at 16px |
| Rail | Full-screen Dialog with the tab strip under its header |
| Menus | Full-screen ActionMenu with 40px items |
| Pickers | Full-screen SelectPanel with Cancel and Save |
| Dialogs with fields | Full screen, footer buttons large |
| Confirmations | ConfirmationDialog centred, 320px wide |
| Messages | Banner at the top of the content with stacked actions. InlineMessage beside its control |
| Settings and admin | `/settings` shows the NavList. Each settings page shows a parent link "Settings" |
| Tables | The name column, one key value column and the action column. A wider table scrolls sideways inside its region |
| Editor | Section 8 |
| Inputs | 16px on iOS (5.3) |
| Safe areas | Markspace keeps `env(safe-area-inset-*)` padding on the app header, full-screen overlays and the keyboard bar. Primer's CSS uses it only at the bottom of full-screen anchored overlays |

## 8. Editor on Phones

This section records the Primer guidance for the editor on phones, the structures that break that guidance, and the structure Markspace uses on narrow viewports.

### 8.1 Primer Guidance

| # | Guidance | Source |
| --- | --- | --- |
| 1 | "App header is never fixed to the top of the viewport. It scrolls with the rest of the page." | Layout foundations |
| 2 | The page scrolls as a whole, and the content region is not a separate scroll container. From 768px a pane is sticky with `max-height: 100vh` and scrolls inside itself | PageLayout source, split page guidance |
| 3 | Below 768px nothing in PageLayout is sticky, and panes stack or become sheets | PageLayout source, layout foundations |
| 4 | "Ensure that sticky or fixed areas (like headers or panes) do not block keyboard focus as users Tab through the page" | PageLayout accessibility |
| 5 | An open Dialog locks page scrolling with `overflow: hidden` on `body` and scrolls its own body. Dialogs with inputs are full screen on narrow viewports | Dialog source and guidelines |
| 6 | Heights use `100dvh`, with `100vh` fallbacks under `@supports (height: 100dvh)` | Overlay, ActionMenu and Dialog source |
| 7 | Targets are at least 24px, and 44px is recommended on mobile. Use the large 40px controls rather than stretching a 32px control. A larger hit area comes from an invisible pseudo-element | Responsive foundations, Primer CSS `minTouchTarget` |
| 8 | Pointer media queries are unreliable. Layout changes follow viewport ranges | Responsive foundations |
| 9 | Pages reflow at 320 × 256 and 200% zoom without horizontal scrolling. Text containers have no fixed heights | Responsive foundations, text resize guidance |
| 10 | `user-scalable=no` is never used. Portrait and landscape both work. Gestures have a button or keyboard alternative | App-like web experiences |
| 11 | Motion runs only under `prefers-reduced-motion: no-preference`. Minor animations stop within 5s | Motion guidance |
| 12 | Hover and tooltips do not exist on touch. Nothing may depend on them | Responsive foundations, Tooltip accessibility |
| 13 | No toasts. Among the listed problems, they cover controls near the bottom of the screen | Accessible notifications |

Primer has no guidance on the on-screen keyboard, `visualViewport` or text editors. Markspace defines the keyboard bar and its placement in `ui.md` rule 10.

### 8.2 Structures That Break Primer Guidance

Each structure below breaks the numbered guidance in 8.1. The client does not use them.

| Structure | Guidance it breaks |
| --- | --- |
| `body { height: 100%; overflow: hidden }` inside a `100dvh` frame with `overflow: hidden`, so the document never scrolls | 2 |
| A scroll container around the main column, and a second one inside it around CodeMirror with `overscroll-behavior: contain` | 2, 3 |
| An app header inside a fixed frame, hidden in focus mode with a negative top margin | 1 |
| A fixed bottom note action bar with `backdrop-filter: blur(14px)` | 1, and PageHeader action placement |
| A status line fixed above the keyboard bar | 13 |
| The rail as a fixed bottom sheet 80dvh tall with its own scroll | 5, 6 |
| Content padding and heights in `vh`, such as a bottom padding of 30vh or images at `max-height: 70vh` | 6, 9 |
| Action rows inserted into the keyboard bar in place of a menu | ActionMenu full screen (7.1) |

### 8.3 Required Structure on Narrow Viewports

| Aspect | Rule |
| --- | --- |
| Scroll container | The document. `html` has `height: 100%` and `body` has `min-height: 100%`, and neither sets `overflow`. The layout root, `.main` and `.ms-editor` have no fixed height and no `overflow`. CodeMirror keeps `.cm-scroller { overflow: visible }`, so the document scrolls the note |
| Minimum height | Layout root `min-height: 100vh`, and `100dvh` under `@supports` |
| App header | Static. It scrolls away |
| Editor bar | `position: sticky; top: 0`, 48px, the only sticky element above the note |
| Focus visibility | `html { scroll-padding-top: 48px; scroll-padding-bottom: var(--ms-kbar-space); }`. `EditorView.scrollMargins` returns the same values |
| Keyboard bar | A Markspace element pinned to the bottom of the visual viewport, sized per 5.10. `scroll-padding-bottom` keeps the caret line above it |
| Page actions | In the PageHeader. No fixed bottom bar |
| Messages | Banners in the page flow above the note. No fixed status line |
| Menus and pickers | Full-screen ActionMenu and SelectPanel, not rows inside the keyboard bar |
| Rail | Full-screen Dialog. While it is open the document does not scroll and the Dialog body does |
| Heights | `dvh` with a `vh` fallback. No `vh` values in content padding. The space under the note equals the keyboard bar height plus 16px |
| Text | Note text 16px. Inputs 16px on iOS |
| Targets | Keyboard bar and PageHeader controls in the large size, with 44px hit areas |
| Motion | Chrome fades only under `prefers-reduced-motion: no-preference`. Sheets slide in over 250ms, with no animation under reduced motion |
| Checks | 390 × 844 and 320 × 256 portrait, 844 × 390 landscape, 200% zoom, each with the keyboard open and closed |

## 9. Accessibility

### 9.1 Focus

| Rule | Value |
| --- | --- |
| Focus ring | 5.0. Never removed without a visible replacement |
| Colour | `--focus-outlineColor`, pink 5 in both themes, at least 3:1 against every background it meets (2.4) |
| Dialogs, sheets, full-screen menus | Focus moves in, stays trapped, and returns to the trigger |
| Soft navigation | Focus moves to the page `h1` (`tabindex="-1"`), and `document.title` updates |
| Added content | Focus moves to the first new item after "Load more" and "Show more" |
| Removed content | Focus moves to the next item, then the previous item, then the list heading. Never to `body` |
| Filtering | Focus stays in the filter input, and the result count is announced |
| Sticky areas | `scroll-padding` keeps focused elements out from under sticky bars |
| Forced colours | 1px transparent outlines become visible system outlines |

### 9.2 Target Size

| Context | Minimum |
| --- | --- |
| Every interactive element | 24 × 24px (WCAG 2.5.8), as Primer requires |
| Narrow viewports and coarse pointers | 44 × 44px, from large controls or `::before` hit areas (`--control-minTarget-coarse`) |
| Token remove buttons | Only large and xlarge Tokens are removable, because smaller remove buttons are under 24px |
| Spacing between touch targets | 8px |

### 9.3 Contrast

| Element | Minimum |
| --- | --- |
| Text | 4.5:1. Primer's light and dark themes pass, and so do the accent pairs (2.4) |
| Input borders against their background | 3:1. Checkboxes and radios use `--control-borderColor-emphasis` |
| Icons in IconButtons and other meaningful icons | 3:1 |
| Selected and current indicators | 3:1, and never colour alone: weight 600, a bar, a check or `aria-current` |
| Focus ring | 3:1 |
| Disabled controls | Exempt, and avoided |
| Avatar initials | 4.5:1 against the stored colour (5.8) |
| UnderlineNav and UnderlinePanels underline | The underline follows `--borderColor-accent-emphasis` and reaches 4.98:1 in light and 4.14:1 in dark. The weight-600 label still carries the state |

Status is never shown by colour alone. Labels, Banners and InlineMessages always carry text or an icon.

### 9.4 Reduced Motion

| Rule | Value |
| --- | --- |
| Transitions and animations | Written inside `@media (prefers-reduced-motion: no-preference)` |
| Spinner | Keeps rotating |
| Skeleton and ProgressBar shimmer | Off |
| Dialog, sheet and overlay entry | No animation |
| JavaScript motion | `prefersReducedMotion()` in `ui.js` guards smooth scrolling and focus-mode fades |
| Flashing | Nothing flashes more than 3 times a second |

### 9.5 ARIA Patterns

| Component | Roles and attributes | Keys |
| --- | --- | --- |
| Button | `button`. `aria-pressed` for toggles. `aria-disabled` instead of `disabled`. `aria-haspopup` and `aria-expanded` when it opens a menu | Enter, Space |
| IconButton | `aria-label`. The Tooltip text is referenced by `aria-labelledby` | Enter, Space. Escape closes the tooltip |
| ActionMenu | Trigger with `aria-haspopup="true"` and `aria-expanded`. `menu`, `menuitem`, `menuitemradio`, `menuitemcheckbox`, `group` with `aria-labelledby`, `separator` | 5.5 |
| SelectPanel | Trigger with `aria-haspopup="dialog"`. `dialog` with `aria-labelledby`. `listbox` and `option` with `aria-selected`. `aria-activedescendant` on the filter input | Arrows, Home, End, Enter, Escape |
| Autocomplete | `combobox` with `aria-expanded`, `aria-controls`, `aria-autocomplete="list"` and `aria-activedescendant`. `listbox`, `option` | Down, Up, Enter, Escape |
| Dialog | `dialog` element with `aria-modal="true"`, `aria-labelledby` on the `h1`, `aria-describedby` on the subtitle | Tab stays inside. Escape closes |
| ConfirmationDialog | `role="alertdialog"`, `aria-labelledby`, `aria-describedby` | Escape cancels |
| Tooltip | `popover`, `aria-hidden="true"`, referenced by `aria-labelledby` or `aria-describedby` | Escape hides |
| Banner | `section` with `aria-label`, `h2` title. Receives focus when essential | |
| InlineMessage | Text beside its control, linked with `aria-describedby` when it describes a field | |
| Status region | `role="status"`, `aria-live="polite"`, present from page load | |
| NavList | `nav` named by its heading, `aria-current="page"` | Tab, Enter |
| TreeView | `tree`, `treeitem`, `group`, `aria-level`, `aria-expanded`, `aria-current`, `aria-selected`, `aria-multiselectable` | 5.1 |
| UnderlineNav | `nav` with a hidden `h2`, links with `aria-current="page"`. The More menu trigger has `aria-expanded` | Tab, Enter |
| UnderlinePanels | `tablist`, `tab` with `aria-selected` and `aria-controls`, `tabpanel` with `aria-labelledby` | Left, Right, Home, End, Enter, Space |
| SegmentedControl | `ul` with `aria-label`, `button` with `aria-current` | Tab, Enter, Space |
| Breadcrumbs | `nav aria-label="Breadcrumbs"`, `ol`, `aria-current="page"` | |
| Pagination | `nav aria-label="Pagination"`, `aria-current="page"`, named page links | |
| PageHeader | `h1`, no landmark role (5.2) | |
| DataTable | `table` named by `aria-labelledby`, `th scope="col"` and `th scope="row"`, `aria-sort` on sortable headers, a scroll wrapper with `role="region"`, `tabindex="0"` and `aria-label` | Arrows scroll the focused wrapper |
| FormControl | `label for`, `aria-describedby` for caption and validation, `aria-invalid`, `required` | |
| CheckboxGroup, RadioGroup | `fieldset` and `legend` | Space toggles a checkbox. Arrows move between radios |
| ToggleSwitch | `button` with `aria-pressed`, `aria-labelledby` and `aria-describedby` | Space, Enter |
| ProgressBar | `progressbar` with `aria-valuenow`, `aria-valuemin`, `aria-valuemax` and `aria-valuetext` | |
| Spinner | SVG `aria-hidden`, visually hidden label | |
| Timeline | `list`, `listitem`. Meaningful badges have an `aria-label` | |
| CounterLabel | The visible number is `aria-hidden`, followed by a hidden copy such as "(12)" | |
| Label, Token | `span`, `a` or `button` by purpose. The remove button of a removable Token names the Token | Backspace or Delete on a focused removable Token |
| Avatar | `img` with `alt=""` beside a name, otherwise the name as `alt` | |
| Details | `details` and `summary` | Enter, Space |
| Keyboard bar, selection toolbar, viewer toolbar | `toolbar` with `aria-label` | Left, Right, Home, End |
| Overflowing pane or table region | `role="region"`, `tabindex="0"`, `aria-label` | |

## 10. Rules in Force

Each row names a topic where Primer's guidance differed from an earlier Markspace rule, and gives the rule Markspace follows. `ui.md`, `files.md`, `plugins.md` and `assistant.md` describe the same behaviour. `ui.md` rules that do not appear here stay in force.

| # | Topic | Rule |
| --- | --- | --- |
| 1 | Accent colour | The accent is fuchsia from Primer's `pink` scale (2.4), in place of Primer's blue. Links, focus, selected and checked states, the UnderlineNav underline and the sponsors role use it |
| 2 | Primary button colour | Primary buttons use `pink` scale values (2.4), in place of Primer's green. Green marks success status only |
| 3 | Stacked overlays | Up to 2 dialogs may be open, one over the other. Menus, pickers and tooltips open over sheets and dialogs |
| 4 | Inline before overlay | Menus, pickers and confirmations are overlays. A task that needs the whole screen is a full-screen Dialog with a close button, not a separate view with Back. Inline forms and row details expand in place |
| 5 | Undo timing | Undo stays until the next action in that place. No message has a timer |
| 6 | Revoke link | Revoke link opens a ConfirmationDialog |
| 7 | Permanent actions | Permanent actions and the `plugins.md` `button` `confirm` field open a ConfirmationDialog with a danger button. There are no two-step buttons |
| 8 | Hover tooltips | IconButtons show a Tooltip on hover and keyboard focus. Link previews on hover are off by default |
| 9 | Menus on phones | ActionMenu opens full screen below 768px. Menus never nest, and there are no inline action rows |
| 10 | Toasts | There are no toasts. Background results use Banners |
| 11 | Status line | There is no status line. The avatar sync badge shows sync state (5.7), and announcements use the status region |
| 12 | Failed save message | A failed save shows a critical Banner with Retry |
| 13 | Phone action bar | Page actions sit in the PageHeader, and the app header scrolls with the page. There is no bottom action bar |
| 14 | Borders | Boxes, panes, tables, lists and dialog headers use 1px borders and dividers |
| 15 | Badges | Counters are neutral CounterLabels and count only things that need action |
| 16 | Motion | Motion uses Primer's durations, which are 200ms for state changes and overlays, 250ms for sheets and 80ms for buttons, all under reduced-motion guards |
| 17 | Settings autosave | Text inputs, checkbox groups, radio groups and native selects wait for an explicit Save. Toggles, segmented controls and custom single selects save on change, with no "Saved" indicator |
| 18 | Rename | A Save button sits beside the name field. Enter saves and Escape cancels |
| 19 | Destination picking | Move to and Copy to open a Dialog with a TreeView of folders |
| 20 | Pickers | Models use a SelectPanel, folders a Dialog with a TreeView, and people and notes an Autocomplete under the input |
| 21 | Sheets over sheets | The rail on narrow viewports is a Dialog, and a second Dialog may open over it |
| 22 | Full-screen tasks | Full-screen tasks, such as image editing, are Dialogs with a close button |
| 23 | App frame and scrolling | The app header scrolls with the page, the page scrolls as a whole, and only a split-page pane scrolls on its own (8.3) |
| 24 | Right rail | The rail is a PageLayout pane at the end of the layout (5.1) |
| 25 | Highlights and tracked changes | Tracked insertions use `--diffBlob-additionWord-bgColor` and deletions `--diffBlob-deletionWord-bgColor`. Text highlight uses `--highlight-neutral-bgColor`, which row 44 moves to the `orange` scale |
| 26 | Type sizes | Body text is 14px with line height 1.5, note text 16px with line height 1.5, and the note title uses PageHeader large |
| 27 | Label case | Labels use sentence case, never all caps |
| 28 | Link underline | Links are underlined, or carry another signifier |
| 29 | Wikilinks | Wikilinks are text in `--fgColor-link` without a pill |
| 30 | Avatars | Avatar keeps Primer's round box and 1px ring. Markspace has no profile images, so the Avatar shows initials on the stored colour (5.8) |
| 31 | Sidebar fill | Panes use the page background with a divider |
| 32 | Emoji | Interface text uses no emoji. The emoji a person chooses for a space or note is their content |
| 33 | Disabled controls | Controls use `aria-disabled` or the inactive style instead of `disabled`. Submit buttons are never disabled |
| 34 | Placeholders | Every field has a visible label. Placeholders never replace labels |
| 35 | Native validation | Forms use `novalidate` and Primer validation messages (6.6) |
| 36 | `title` attributes | Tooltips appear only on interactive elements. Dates use RelativeTime |
| 37 | Shortcuts | Focus mode uses `Ctrl Shift .`, because Primer advises against Ctrl or Cmd with F. Markspace has no `?` shortcut dialog (6.10) |
| 38 | Loading delay | No loading indicator appears before 1s |
| 39 | Tree row menu button | Tree rows hold no control besides the chevron. Tree actions open from the context menu, Shift+F10, the Menu key and the opened item's PageHeader menu |
| 40 | Rail tabs | Rail tabs have text labels and no icons |
| 41 | Tables on phones | Tables hide less important columns or scroll sideways in a named, focusable region |
| 42 | Large tables | Large tables page from 20 rows. The CSV viewer pages its rows and offers Download for the full file |
| 43 | Neutral colours | Neutral tokens keep Primer's lightness with a small fuchsia tint at OKLCH hue 330 (2.4) |
| 44 | Attention colour | Attention and `--highlight-neutral-bgColor` use Primer's `orange` scale. Markspace uses no yellow or amber (2.4) |
| 45 | Sync status | A badge on the Avatar button shows sync state, with a tick when synced, a dot when pending or syncing, and a warning when offline or on error. The Avatar's accessible name carries the state. The account menu starts with the sync status line and "Sync now". There is no separate sync overlay, and server pages have no badge (5.7) |
| 46 | Version history | The History rail tab shows Current and the latest 4 versions and opens a full-screen Dialog through its rows and "Show diff", as does "Version history" in the note's More menu. The Dialog lists the versions beside a read-only CodeMirror merge view, side by side from 768px and unified below with unchanged regions collapsed. Download and "Restore this version" sit in the footer (5.10) |
| 47 | Semantic colours | Red and green appear only for status, such as errors, destructive actions, success messages and diffs. Interactive states use the accent |
| 48 | App header New page | The app header New page is a Button primary. It shows only when the PageHeader of the view has no New page action of its own (5.1 App header, row New page) |

## 11. Implementation Map

The tables below list the files that implement each part of this document. Paths start at `internal/web`, except repository root paths, which the rows name.

### Tokens and Base CSS

| Files | Implements |
| --- | --- |
| `static/vendor/primer/primitives-11.10.0.css`, `light-11.10.0.css`, `dark-11.10.0.css`, `LICENSE-primitives` | The vendored files in 2.1, unchanged |
| `static/theme.css` | The theme override in 2.4, which covers the pink accent, the pink primary button, attention in orange and the neutral tint |
| `static/css/base.css` | The temporary alias block (2.5), `color-scheme` (2.3), element defaults, the type scale (3.2) and reduced motion (9.4) |
| `templates/base.html` and `shell.html`, `head` sections | Stylesheet links in the order of 2.2, and the theme attributes and theme script from 2.3 |
| `saveThemePreference` in `static/js/shell.js`, the theme handler in `static/app.js`, `theme_switch` in `templates/base.html` | The theme choice (2.3) |
| `web.go` | The embed pattern for `templates`, `static/css/*.css`, the scripts, `static/icons` and `static/vendor` |
| `pwa.go`, `static/sw.js` | The `__MARKSPACE_ASSETS__` precache list, which holds every stylesheet |

### Icons

| Files | Implements |
| --- | --- |
| `static/vendor/octicons-19.36.0/*.svg`, `LICENSE` | The 16px and 24px files of every icon named in 4.3 and section 5 |
| `static/js/octicons.js` | Path data from `data.json` for the same icons |
| `ICON_MAP`, `ICON_NAMES` and `icon()` in `static/js/ui.js` | Octicon rendering with the old-name map from 4.3 |
| `octicon.go`, registered in `render.go`, and `octicon_test.go` | The template function from 4.2 and its test |

### Shared Components

| Files | Implements |
| --- | --- |
| `static/js/ui.js` | `button`, `iconButton`, `tooltip`, `openMenu` and `actionMenu` (ActionMenu), `selectPanel`, `autocomplete`, `openDialog` (Dialog), `confirmDialog` (ConfirmationDialog), `anchoredOverlay`, `banner`, `inlineMessage`, `announce`, `undoRow`, `setBusy`, `spinner`, `skeleton`, `progressBar`, `blankslate`, `counterLabel`, `label`, `token`, `avatar`, `formControl`, `showFieldErrors` with the error summary, `editInPlace`, `expandRow`, `treeView`, `segmentedControl`, `toggleSwitch`, `details`, `relativeTime`, `createDropzone` and `uploadList` |
| `static/app.js` | Server-page behaviour, which covers ActionMenu keyboard handling for `[data-action-menu]`, ConfirmationDialog for `data-confirm`, undo rows for `data-undo-*` and Tooltips |
| `static/css/components.css`, `pages.css`, `forms.css`, `markdown.css` | Sections 5.0, 5.2 (PageHeader, section headings, boxes, DataTable, Pagination), 5.3, 5.4 to 5.9, and the markdown rules in 5.10 |
| `templates/partials/flash.html`, `partials/pagination.html`, `partials/avatar.html` | Banner, Pagination and Avatar on server pages |
| `static/vendor/relative-time-element-5.3.1.js`, date helpers in `render.go` | RelativeTime (5.9) |
| Repository root `test/client/ui.test.mjs` | Unit tests for the `ui.js` helpers |

### Shell and Navigation

| Files | Implements |
| --- | --- |
| `static/js/shell.js`, `static/js/views.js` | Layout, app header, sidebar pane and drawer, NavList, TreeView, rail pane and rail Dialog, UnderlinePanels, PageHeader on shell routes, file view header, quick switcher Dialog, and the move and copy Dialog |
| `static/css/shell.css` | Section 5.1, and the document scrolling structure in 8.3 |
| `templates/base.html` (`topbar`, `app_sidebar`, `quickopen`), `shell.html`, `partials/tree.html` | Server header, sidebar and tree |

### Panels

| Files | Implements |
| --- | --- |
| `static/js/panels.js`, `static/css/panels.css` | Outline, Comments, Suggestions, History, Share, Files, AI and Info panels (5.10) |
| `templates/partials/comments.html`, `partials/share.html`, `partials/meta.html`, `note.html` | Server note page and its panels |
| `notes.go` | Server fallbacks for confirmations and undo on note actions |

### Forms and Settings Pages

| Files | Implements |
| --- | --- |
| `templates/profile.html`, `tokens.html`, `sessions.html`, `space/settings.html`, `spaces.html`, `space/home.html`, `trash.html`, `starred.html`, `recent.html`, `shared.html`, `tag.html`, `search.html` | SplitPageLayout with NavList for settings, forms per 5.3 and 6.6, lists and tables |
| `templates/login.html`, `signup.html`, `setup.html`, `public.html`, `public/item.html`, `public/password.html`, `error.html`, `offline.html`, `offline/files.html` | Interstitial pages, public pages, and the Blankslate error and offline pages |
| `templates/partials/settings.html` | Settings NavList |
| `static/css/settings.css` | Rules specific to these pages |
| `user.go`, `space.go`, `auth.go`, `browse.go`, `public.go` | Explicit Save handling, error summaries, and confirmation pages that work without scripts |

### Admin Pages

| Files | Implements |
| --- | --- |
| `templates/admin.html`, `admin/users.html`, `admin/ai.html`, `admin/settings.html`, `admin/usage.html`, `admin/module.html`, `ai.html`, `usage.html`, `partials/admin.html`, `partials/ai.html`, `partials/usage.html` | Administration SplitPageLayout, DataTables, forms and budgets |
| `static/js/ai/admin.js`, `static/css/admin.css`, `static/css/usage.css` | AI routing and usage pages. `usage.css` colours the chart series with `--data-*` tokens |
| `admin.go`, `usage.go`, `usagechart.go` | Server code for these pages |

### Editor Chrome

| Files | Implements |
| --- | --- |
| `static/js/editor.js`, `static/js/find.js`, `static/js/ai/aitab.js`, `askbar.js`, `common.js`, `composer.js`, `imageactions.js`, `tracked.js` | Editor bar, SegmentedControl, keyboard bar, find bar, selection toolbar, slash menu and link completion, live preview, ask bar, tracked changes, and the editor side of 8.3 |
| `static/css/editor.css` | Section 5.10 editor rules |
| `templates/editor.html` | Server editor fallback |

`shell.css` and `editor.js` both implement 8.3. `shell.css` holds the document scrolling structure. `editor.js` holds `placeKbar` and the `EditorView.scrollMargins` extension.

### File Views

| Files | Implements |
| --- | --- |
| `static/js/viewers/common.js`, `csv.js`, `image.js`, `kind.js`, `media.js`, `other.js`, `pdf.js`, `text.js` | Viewer toolbars, PDF page field and thumbnails pane, find, CSV DataTable, and the other-file and error Blankslates |
| `static/css/files.css` | File grid, file rows and viewer rules |

### Plugin UI Renderer

| Files | Implements |
| --- | --- |
| `static/js/plugins.js` | The node mapping in 5.11 using the `ui.js` helpers, with the size limits and escaping from `plugins.md` |
| `templates/module.html` | Plugin page layout |
| Repository root `test/client/plugins.test.mjs` | Renderer tests |

### Checks

| Files | Implements |
| --- | --- |
| Repository root `test/browser` | Browser checks of this document. `spec.json` lists expected Primer values, and each entry cites the section and table row it comes from |
| Repository root `test/client`, `web_test.go` | Unit tests for the client modules and the server pages |
