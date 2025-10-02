# Cincinnati

Cincinnati is the name of the concept and technology that OpenShift uses to define platform updates. _This_
example (probably somewhat simplistically) maps OpenShift's Cincinnati concepts to layered product operators.

Here, we define an API for layered products to use that describe their product at a high level. This API
answers questions like:
- What is your product name?
- What are the minor versions of your product?
- For each of the minor versions of your product, what are the support lifecycle dates from General Availability to
  End-of-Life.
- For each of the minor versions of your product, what is the minimum version that can upgrade into this minor version?
- For each of the minor versions of your product, what versions of OCP are supported.
- What are all of the released bundle images that make up these minor versions?

With this information, we can derive a holistic view of product's update lifecycle super-imposed on the OpenShift
lifecycle. We can also provide guarantees that span across OCP versions, so that customers no longer need to cross
their fingers and hope when they click an update button.

# Opinions

However, in order to do this we need to agree on some basic processes. This implementation makes the following
assumptions:
1. Users never want to receive an update that regresses on a bug fix they have already consumed.
2. Users never want to update into a new minor version if that new minor version is already end-of-life.
3. Users never want to update into a new version if that version has a "worse" support lifecycle state.
   For example, never update from a fully supported version to a version that is in maintenance.
4. Users never want automatic updates into a different major version because major versions, by definition, include
   breaking changes.

# Details

With this information and those opinions defined, we can fairly easily build an upgrade graph that can be interrogated
to plan an update of both OCP and all of a customer's layered products.

# Open Questions

1. Current logic for building edges inevitably causes some backport releases to have no outgoing edges
   (i.e. they are "heads") despite higher versions existing in the graph. Do we need to worry about this?
2. Packages that prefer post-platform-update node updates are typically platform-aligned. During EUS-to-EUS updates,
   are these packages expected users to perform package updates in the intermediate platform version?
    - Example: Say an operator has versions, 4.16, 4.17, and 4.18, coinciding with OCP versions. Can customers update
      their clusters from 4.16, through 4.17, and finally to 4.18 all while keeping their operator on a 4.16 supported
      version? Or are this packages trying to require that customers pause the EUS-to-EUS update on 4.17 in order to
      perform an update from the 4.16-supported operator to the 4.17-supported operator?

   Joe's opinion: EUS-to-EUS platform updates MUST not require operator updates in the odd-numbered OCP version.
