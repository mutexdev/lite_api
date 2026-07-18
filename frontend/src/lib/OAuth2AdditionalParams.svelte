<script lang="ts">
  import type { main } from '../../wailsjs/go/models'
  import KeyValueTable from './KeyValueTable.svelte'

  type Field = 'name' | 'value' | 'enabled'
  type SendIn = 'headers' | 'queryparams' | 'body'

  export let title = ''
  export let params: main.OAuth2AdditionalParam[] = []
  export let onAdd: (sendIn: SendIn) => void | Promise<void> = () => {}
  export let onChange: (index: number, field: Field, value: string | boolean) => void | Promise<void> = () => {}
  export let onRemove: (index: number) => void | Promise<void> = () => {}

  const groups: { id: SendIn; label: string }[] = [
    { id: 'headers', label: 'Headers' },
    { id: 'queryparams', label: 'Query' },
    { id: 'body', label: 'Body' }
  ]

  function normalizeSendIn(value?: string): SendIn {
    const normalized = (value || 'body').toLowerCase()
    if (normalized === 'headers' || normalized === 'header') return 'headers'
    if (normalized === 'queryparams' || normalized === 'query' || normalized === 'url') return 'queryparams'
    return 'body'
  }

  function entries(sendIn: SendIn) {
    return (params ?? []).map((param, index) => ({ param, index })).filter(({ param }) => normalizeSendIn(param.sendIn) === sendIn)
  }

  function rows(sendIn: SendIn) {
    return entries(sendIn).map(({ param }) => ({
      name: param.name ?? '',
      value: param.value ?? '',
      enabled: param.enabled ?? true,
      secret: param.secret ?? false,
      description: param.description ?? ''
    }))
  }

  function originalIndex(sendIn: SendIn, visibleIndex: number) {
    return entries(sendIn)[visibleIndex]?.index ?? visibleIndex
  }
</script>

<div class="oauth2-extra">
  <h4>{title}</h4>
  <div class="oauth2-extra-grid">
    {#each groups as group}
      <section class="oauth2-param-group">
        <h5>{group.label}</h5>
        <KeyValueTable
          rows={rows(group.id)}
          onAdd={() => onAdd(group.id)}
          onChange={(index, field, value) => onChange(originalIndex(group.id, index), field, value)}
          onRemove={(index) => onRemove(originalIndex(group.id, index))}
        />
      </section>
    {/each}
  </div>
</div>
