// Package judge asks a language model whether the answers of a Tutti Frutti
// category are valid examples of it. It is the only code that talks to the
// model; the game engine stays pure and the verdicts it stores are advisory.
//
// The provider is Cloudflare Workers AI on the Free plan: 10,000 Neurons a
// day, after which Cloudflare refuses the call (nothing is ever billed unless
// the account is upgraded), and no retention or training on the content. The
// package adds its own guard rails so the shared allowance is not burnt: a
// daily Neuron budget kept below the allowance, a global rate limit, a limit
// on parallel calls and a cache of verdicts (answers repeat a lot).
//
// The answers are typed by players, so they are untrusted: they travel as JSON
// data in the user message, the system prompt tells the model not to follow
// instructions inside them, and every field of the reply is validated.
package judge
