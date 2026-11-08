#!/bin/sh
# Read-only inspection of this task's two paid-activation race commands.
ps -axo pid=,etime=,args= | awk '(/[g]o test/ || /[w]allet[.]test/) && /TestAccountPaidActivation(PreservesConcurrentLocalChanges|SelectionChangeDoesNotChangeRootIdentity)/ { print; found=1 } END { if (!found) print "No matching test launcher or test binary found" }'
