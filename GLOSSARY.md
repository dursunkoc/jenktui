# Glossary

**Job Catalog**
The complete flat list of buildable Jobs found under the configured Parent Job (or the Jenkins root when none is configured), with folders expanded. It is the source from which Collections are curated.

**Job**
A buildable item on the Jenkins server, identified by its full path (e.g. `team/app/build`). Folders are not Jobs.

**Parent Job**
The configured Jenkins folder or Multibranch Project whose descendants make up the Job Catalog. When not configured, the Jenkins root is used.

**Collection**
A named, ordered group of Jobs curated by the user from the Job Catalog. Builds are triggered from a Collection.
_Avoid_: list, sublist.

**Overview**
The screen that lists every Collection with the number of its Jobs whose latest build failed (or is unstable), is running or is waiting in the queue, and how many are missing from Jenkins. It polls on its own while shown and opens a Collection's Run Monitor.

**Jump**
The quick way to a Collection or a Job from any screen after login: a filterable list of both (the palette). The screen it was opened over pauses and resumes when it closes. Choosing a Collection opens its Run Monitor; choosing a Job opens the Job Catalog filtered to it.

**Trigger**
Requesting a new build of one or more Jobs, optionally supplying build parameters.

**Common Parameters**
Build parameters shared by name and type across the Jobs selected in one Trigger, entered once and applied to all of them. Any Job can override them: in its per-job step, or by editing it from the summary.

**Run Monitor**
The live screen showing the latest build of every Job in one Collection, with stage information where available. A running row shows its elapsed time against Jenkins' estimate, a finished row how long ago it started, and a row with history its **History Dots**. Each Collection has its own Run Monitor, kept for the session; naming a Collection on the command line opens its Run Monitor right after login. The first time it is opened from a Collection it hides successful Jobs, and the user can show them again; reopening keeps that choice. Deleting the Collection drops its Run Monitor. A Trigger from a Collection adds the new builds to that Collection's Run Monitor. From it the user can open a build, or a Job with no build yet, in the browser, copy its address, jump to the next failed Job, and open the details of a build: its stages and steps, why it started (its causes) and its commits. The details of a failed build open on its failed stage, and its log opens at its end.

**Console Log**
A modal in a build's details showing the log of the whole build or of one stage or step, with line numbers. It follows new output while the build runs, can be searched with the same wildcards as a list filter, highlights error lines, can wrap long lines and can be saved to a file.

**Test Report**
The failed, passed and skipped test counts Jenkins keeps for a finished build, shown in its details when there is one, with the list of failed tests and their stack traces.

**Abort**
Stopping a running build, or cancelling a queued one, after the user confirms.

**Rerun**
A new Trigger of a build's Job with the build's parameter values filled in. Jenkins does not keep password values, so they are asked for again. The new build joins the same Run Monitor.

**Notification**
A terminal bell and a desktop message sent when a build the Run Monitor saw queued or running finishes. The **Notify mode** (failures, all or off) says which results notify. It happens only while that Run Monitor is the screen on show.

**History Dots**
The results of a Job's last five builds as glyphs at the end of its Run Monitor row, newest last, so a flaky Job stands out.

**Multibranch Project**
A Jenkins item that owns one Job per source-control branch. The Job Catalog lists its branches as Jobs, not the project itself.

**Branch Filter**
A list of glob patterns, matched case-sensitively against decoded branch names, that limits which branches of Multibranch Projects are shown when browsing or picking Jobs. It can be switched on or off at any time. It never affects other Jobs, and it does not remove branches already in a Collection.

**Missing Job**
A member of a Collection that no longer exists on the Jenkins server. It stays in the Collection, is marked as missing, and is skipped when triggering. It is only removed when the user removes it.

**Status Filter**
A view limit that shows only Jobs whose latest build has one chosen status (for example failed). It combines with text filtering, and only affects what is shown.

**Help Panel**
The overlay that lists every key of a screen in groups, available on screens whose footer ends in `? all keys`.

**Relative Name**
How a Job is displayed: its full path without the Parent Job's prefix, folders separated by a muted `›`. Filtering, saved Collections and the Jenkins API still use the full path.
