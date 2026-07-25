<script lang="ts">
  import type { Snippet } from 'svelte'

  export let protocol = 'http'
  export let method = 'GET'
  export let methods: string[] = []
  export let busy = ''
  export let urlField: Snippet
  export let onMethodChange: (value: string) => void
  export let onGenerateGrpcurl: () => void | Promise<void>

  $: isGrpc = protocol === 'grpc'
  $: isWebSocket = protocol === 'websocket'
  $: grpcMethod = method === 'CALL' ? '' : method
</script>

<section class="protocol-request-line" aria-label="Request target">
  <div class="request-line" class:grpc={isGrpc} class:ws={isWebSocket} class:http={!isGrpc && !isWebSocket}>
    {#if isGrpc}
      <div class="grpc-server">{@render urlField()}</div>
      <input class="grpc-method" aria-label="gRPC method" placeholder="package.Service/Method" value={grpcMethod} on:input={(event) => onMethodChange(event.currentTarget.value)} />
      <button class="grpcurl-action" type="button" title="Generate grpcurl command" aria-label="Generate grpcurl command" on:click={() => void onGenerateGrpcurl()} disabled={busy !== '' || !method || method === 'CALL'}>grpcurl</button>
    {:else if isWebSocket}
      <span class="method-badge ws" data-method="WS" aria-label="WebSocket protocol">WS</span>
      {@render urlField()}
    {:else}
      <select aria-label="Method" data-method={method} value={method} on:change={(event) => onMethodChange(event.currentTarget.value)}>
        {#each methods as candidate}
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
