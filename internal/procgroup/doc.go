// Package procgroup is the one owner of starting a child in its own process
// group and killing that whole group. A caller that spawns a process it may have
// to stop — doctor's live probes, the host commands a remote node is started
// with — uses Set before Start and Kill to end it, so no grandchild outlives it.
package procgroup
