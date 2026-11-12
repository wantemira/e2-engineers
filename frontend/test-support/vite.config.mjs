import { createInstrumenter } from "istanbul-lib-instrument";
import { defineConfig, mergeConfig } from "vite";
import baseConfig from "../vite.config.ts";

export default defineConfig((env) => {
  if (!process.env.BUDDY_TEST_COVERAGE) return baseConfig(env);

  return mergeConfig(baseConfig(env), {
    plugins: [{
      name: "ai-buddy-test-coverage",
      enforce: "pre",
      transform(code, id) {
        if (!/\/src\/.*\.tsx?$/.test(id) || /\.test\.tsx?$/.test(id)) return;
        const instrumenter = createInstrumenter({
          esModules: true,
          parserPlugins: ["typescript", "jsx"],
          produceSourceMap: true,
        });
        return {
          code: instrumenter.instrumentSync(code, id),
          map: instrumenter.lastSourceMap(),
        };
      },
    }],
    test: { setupFiles: ["./test-support/save-unit-coverage.mjs"] },
  });
});
