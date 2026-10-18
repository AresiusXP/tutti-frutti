import { compile } from '@inlang/paraglide-js';
import { paraglideOptions } from '../paraglide.config.js';

// The Vite plugin injects `import.meta.env.SSR`; outside Vite keep the default.
const options = { ...paraglideOptions };
delete options.isServer;

await compile(options);
