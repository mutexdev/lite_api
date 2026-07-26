<script lang="ts">
  // US-036 — defers CodeMirror off the cold-launch path.
  //
  // CodeEditor statically imports eleven @codemirror/* packages plus the
  // `codemirror` meta-package. Because App.svelte imported it directly, every
  // one of those ended up in the initial chunk, parsed before the first frame,
  // for a user who may never open the Body, Script, Tests or Docs tab.
  //
  // This wrapper is a drop-in: it takes exactly CodeEditor's props and forwards
  // them unchanged, so the nine call sites in App.svelte did not have to change
  // and cannot drift from the real component's signature.
  //
  // WHY A WRAPPER RATHER THAN {#await import(...)} AT EACH CALL SITE: nine
  // copies of the same await block is nine chances to get the fallback, the
  // error branch or the prop list subtly wrong, and a divergence would only
  // show up in whichever tab nobody tested.
  //
  // The module promise is created once at module scope, NOT per instance.
  // Several editors can be mounted at the same time (a GraphQL body renders two
  // side by side); a per-instance import would ask the loader for the same
  // chunk repeatedly, and each new instance would flash its placeholder again
  // even though the code was already in memory.

  type Language = 'json' | 'xml' | 'javascript' | 'markdown' | 'text' | 'graphql'
  type VariableInfo = { name: string; scope: string; resolvedValue: string; secret: boolean; found: boolean; validName: boolean }

  export let value = ''
  export let editorKey = ''
  export let language: Language = 'text'
  export let ariaLabel = 'Code editor'
  export let testId = 'code-editor'
  export let fontSize = 13
  export let onChange: (value: string) => void
  export let variableInfo: VariableInfo[] = []

  const editorModule = import('./CodeEditor.svelte')
</script>

{#await editorModule}
  <!-- Sized like the editor it replaces so the surrounding layout does not jump
       when the chunk lands. aria-busy tells assistive tech this is transient
       rather than an empty editor. -->
  <div class="code-editor-loading" data-testid={testId} aria-label={ariaLabel} aria-busy="true"></div>
{:then module}
  <svelte:component
    this={module.default}
    {value}
    {editorKey}
    {language}
    {ariaLabel}
    {testId}
    {fontSize}
    {onChange}
    {variableInfo}
  />
{:catch error}
  <!-- A failed chunk load is a real, if rare, condition (an interrupted update
       leaving a stale index.html referencing a chunk that no longer exists).
       Saying so beats an editor that silently never appears. -->
  <div class="code-editor-error" role="alert" data-testid={testId}>
    The editor failed to load: {error instanceof Error ? error.message : String(error)}
  </div>
{/await}

<style>
  .code-editor-loading {
    min-height: 180px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-soft);
  }

  .code-editor-error {
    padding: 12px;
    border: 1px solid var(--danger, #c0392b);
    border-radius: 6px;
    color: var(--danger-strong, #c0392b);
    font-size: 12px;
  }
</style>
