<script lang="ts">
  // US-036 — the Display section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.

  export let appZoomPercentage: number
  export let zoomPercentages: number[]
  export let zoomDefaultPercentage: number
  export let codeFont: string
  export let codeFontSize: number
  export let resetZoomPercentage: () => void
  export let setZoomPercentage: (percentage: number) => void
  export let updateCodeFont: (font: string) => void
  export let updateCodeFontSize: (size: number) => void
</script>

	            <section>
	              <div class="settings-section-header">
	                <h3>Display</h3>
	                <span class="preference-value" data-testid="zoom-percentage-value">{appZoomPercentage}%</span>
	              </div>
	              <div class="font-preference-grid">
	                <label class="field-label" for="code-font-input">Code Editor Font</label>
	                <label class="field-label" for="code-font-size-input">Font Size</label>
	                <input
	                  id="code-font-input"
	                  data-testid="code-font-input"
	                  aria-label="Code Editor Font"
	                  value={codeFont}
	                  autocapitalize="off"
	                  autocomplete="off"
	                  autocorrect="off"
	                  spellcheck="false"
	                  on:input={(event) => updateCodeFont(event.currentTarget.value)}
	                />
	                <input
	                  id="code-font-size-input"
	                  data-testid="code-font-size-input"
	                  aria-label="Font Size"
	                  type="number"
	                  min="1"
	                  max="32"
	                  inputmode="numeric"
	                  value={codeFontSize}
	                  on:input={(event) => updateCodeFontSize(Number(event.currentTarget.value))}
	                />
	              </div>
	              <div class="zoom-preference-row">
	                <label class="field-label" for="zoom-percentage">Zoom</label>
	                <select
	                  id="zoom-percentage"
	                  data-testid="zoom-percentage-select"
	                  aria-label="App zoom"
	                  value={appZoomPercentage}
	                  on:change={(event) => setZoomPercentage(Number(event.currentTarget.value))}
	                >
	                  {#each zoomPercentages as percentage (percentage)}
	                    <option value={percentage}>{percentage}%</option>
	                  {/each}
	                </select>
	                <button
	                  data-testid="zoom-reset-btn"
	                  on:click={resetZoomPercentage}
	                  disabled={appZoomPercentage === zoomDefaultPercentage}
	                >
	                  Reset
	                </button>
	              </div>
	            </section>
