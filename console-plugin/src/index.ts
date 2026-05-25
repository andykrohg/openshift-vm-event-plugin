/*
 * Plugin entry point
 */
console.log('VM Events Plugin: Loading module');
export { default as EventHistoryTab } from './components/EventTimeline';
console.log('VM Events Plugin: Module loaded, EventHistoryTab exported');
