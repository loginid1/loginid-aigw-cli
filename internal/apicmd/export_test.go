package apicmd

// AddAllWithBase lets tests point the commands at a fake server. The real
// binary has no way to change the base; see AddAll.
var AddAllWithBase = addAll
