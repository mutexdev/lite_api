import {defineConfig} from 'vite'
import {svelte} from '@sveltejs/vite-plugin-svelte'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte()],
  build: {
    rollupOptions: {
      output: {
        // US-036. CodeMirror is ~11 packages and is only needed once a user
        // opens Body, Script, Tests or Docs. LazyCodeEditor defers the import;
        // this keeps everything it pulls in inside ONE chunk rather than
        // letting the bundler scatter it across several, so the deferred load
        // is a single request rather than a waterfall.
        //
        // Note this alone would not have helped: a manual chunk still loads
        // eagerly if something in the initial graph imports it statically. The
        // dynamic import in LazyCodeEditor is what actually moves the bytes off
        // the cold-launch path; this only shapes where they land.
        manualChunks(id: string) {
          if (id.includes('node_modules/@codemirror/') ||
              id.includes('node_modules/codemirror/') ||
              id.includes('node_modules/@lezer/')) {
            return 'codemirror'
          }
          return undefined
        },
      },
    },
  },
})
