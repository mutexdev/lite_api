<script lang="ts">
  import type { Snippet } from 'svelte'

  // US-028 — runes.
  type Props = {
    protocol?: string
    method?: string
    methods?: string[]
    busy?: string
    urlField: Snippet
    onMethodChange: (value: string) => void
    onGenerateGrpcurl: () => void | Promise<void>
  }

  let {
    protocol = 'http',
    method = 'GET',
    methods = [],
    busy = '',
    urlField,
    onMethodChange,
    onGenerateGrpcurl
  }: Props = $props()

  // All three are pure derivations of props, so $derived rather than $effect:
  // an effect would recompute after the DOM update instead of before it.
  const isGrpc = $derived(protocol === 'grpc')
  const isWebSocket = $derived(protocol === 'websocket')
  const grpcMethod = $derived(method === 'CALL' ? '' : method)
</script>

<section class="protocol-request-line" aria-label="Request target">
  <div class="request-line" class:grpc={isGrpc} class:ws={isWebSocket} class:http={!isGrpc && !isWebSocket}>
    {#if isGrpc}
      <div class="grpc-server">{@render urlField()}</div>
      <input class="grpc-method" aria-label="gRPC method" placeholder="package.Service/Method" value={grpcMethod} oninput={(event) => onMethodChange(event.currentTarget.value)} />
      <button class="grpcurl-action" type="button" title="Generate grpcurl command" aria-label="Generate grpcurl command" onclick={() => void onGenerateGrpcurl()} disabled={busy !== '' || !method || method === 'CALL'}>grpcurl</button>
    {:else if isWebSocket}
      <span class="method-badge ws" data-method="WS" aria-label="WebSocket protocol">WS</span>
      {@render urlField()}
    {:else}
      <select aria-label="Method" data-method={method} value={method} onchange={(event) => onMethodChange(event.currentTarget.value)}>
        {#each methods as candidate (candidate)}
          <option value={candidate}>{candidate}</option>
        {/each}
      </select>
      {@render urlField()}
    {/if}
  </div>
</section>

<style>
  .protocol-request-line { container-type: inline-size; min-width: 0; }
  .request-line.grpc { grid-template-columns: minmax(0, 1fr) minmax(170px, 0.72fr) auto; }
  .grpc-server { min-width: 0; }
  .grpcurl-action { white-space: nowrap; }
  @container (max-width: 620px) {
    .request-line.grpc { grid-template-columns: minmax(0, 1fr) auto; }
    .grpc-server { grid-column: 1 / -1; }
  }
  @container (max-width: 420px) {
    .request-line.http { grid-template-columns: minmax(0, 1fr); }
    .request-line.ws { grid-template-columns: minmax(0, 1fr); }
    .request-line.ws .method-badge { width: max-content; }
  }
</style>
