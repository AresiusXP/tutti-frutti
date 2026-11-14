/** The parts of the "How to play" guide. The playing and review tips open the guide on their own part. */
export const HELP_SECTIONS = ['basics', 'playing', 'review', 'points'] as const;
export type HelpSection = (typeof HELP_SECTIONS)[number];
