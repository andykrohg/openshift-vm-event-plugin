/*
 * Plugin entry point
 */
console.log('VM Events Plugin: Loading module');
export { default as EventHistoryTab } from './components/EventTimeline';
export { default as EventHistoryListPage } from './components/EventHistoryListTab';
console.log('VM Events Plugin: Module loaded');
