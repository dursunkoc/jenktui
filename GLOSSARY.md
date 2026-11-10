# Glossary

**Job Catalog**
The complete flat list of buildable Jobs found under the configured Parent Job (or the Jenkins root when none is configured), with folders expanded. It is the source from which Collections are curated.

**Job**
A buildable item on the Jenkins server, identified by its full path (e.g. `team/app/build`). Folders are not Jobs.

**Parent Job**
The configured Jenkins folder whose descendants make up the Job Catalog. When not configured, the Jenkins root is used.

**Collection**
A named, ordered group of Jobs curated by the user from the Job Catalog. Builds are triggered from a Collection.
_Avoid_: list, sublist.

**Trigger**
Requesting a new build of one or more Jobs, optionally supplying build parameters.

**Common Parameters**
Build parameters shared by name and type across the Jobs selected in one Trigger, entered once and applied to all of them.

**Run Monitor**
The live screen showing the latest build of every Job in one Collection, with stage information where available. Each Collection has its own Run Monitor, kept for the session. Opened from a Collection it hides successful Jobs by default, and the user can show them again. A Trigger from a Collection adds the new builds to that Collection's Run Monitor.

**Multibranch Project**
A Jenkins item that owns one Job per source-control branch. The Job Catalog lists its branches as Jobs, not the project itself.

**Branch Filter**
A list of glob patterns, matched case-sensitively against decoded branch names, that limits which branches of Multibranch Projects are shown when browsing or picking Jobs. It can be switched on or off at any time. It never affects other Jobs, and it does not remove branches already in a Collection.

**Missing Job**
A member of a Collection that no longer exists on the Jenkins server. It stays in the Collection, is marked as missing, and is skipped when triggering. It is only removed when the user removes it.

**Status Filter**
A view limit that shows only Jobs whose latest build has one chosen status (for example failed). It combines with text filtering, and only affects what is shown.
