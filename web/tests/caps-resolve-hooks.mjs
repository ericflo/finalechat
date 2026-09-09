// Test-only ESM resolve hook: the web sources use bundler-style
// extensionless relative imports ("./clientCaps"), which plain Node cannot
// resolve. Map them to their .ts files so node --test can import the real
// modules via type stripping.
export async function resolve(specifier, context, next) {
  try {
    return await next(specifier, context);
  } catch (err) {
    if (
      err?.code === "ERR_MODULE_NOT_FOUND" &&
      typeof specifier === "string" &&
      /^(\.\.?\/[^.]*|.*\/lib\/[A-Za-z]+)$/.test(specifier) &&
      !specifier.endsWith(".ts") &&
      context?.parentURL?.includes("/web/src/")
    ) {
      return await next(`${specifier}.ts`, context);
    }
    throw err;
  }
}
