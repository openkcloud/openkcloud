================
cyborg-dashboard
================

Horizon plugin that adds a Cyborg (accelerator) management panel to the
OpenStack Dashboard. It surfaces accelerator devices (FPGA, GPU, etc.)
exposed by the Cyborg API, correlates them with deployables and accelerator
requests (ARQs), and shows which instances each device is bound to.

Features
--------

* New ``Accelerator`` panel under the ``Admin`` dashboard.
* Lists accelerator devices with hostname, type, model, usage status, and
  links to the attached instance(s).
* Hides itself automatically if the ``accelerator`` service endpoint is not
  registered in Keystone.

Requirements
------------

* OpenStack Horizon (tested against ``stable/2024.1``).
* A running Cyborg API service registered under the ``accelerator`` service
  type in Keystone.

Installation
------------

Manual install (development / classic deployments)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

From a source checkout::

    pip install .

After installation, copy the panel registration files into Horizon's
``enabled/`` directory so Horizon discovers the panel::

    cp cyborg_dashboard/enabled/_21*.py \
       /path/to/openstack_dashboard/local/enabled/

Then collect static assets and compile messages as usual, and restart the
Horizon web server::

    python manage.py collectstatic --noinput
    python manage.py compress --force

kcloud / loci container build
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

This plugin is wired into the kcloud Horizon container image through
``kcloud-loci``. The ``scripts/project_specific/horizon/01_configure_horizon.sh``
script consumes a ``HORIZON_EXTRA_PANELS`` build argument and symlinks every
``<pkg>/enabled/_[1-9]*.py`` it finds into Horizon's
``local/enabled/`` for each listed package.

To include cyborg-dashboard in the image, set the following on the Horizon
build (see ``kcloud-horizon/.github/workflows/docker-build-and-push.yml``)::

    PIP_PACKAGES: >-
      ...
      git+https://github.com/openkcloud/kcloud-cyborg-dashboard@<ref>
    HORIZON_EXTRA_PANELS: cyborg_dashboard

and pass both as ``--build-arg`` to the ``docker build`` invocation. No
modification to ``kcloud-loci`` itself is required because the package ships
its ``enabled/`` directory inside ``cyborg_dashboard/`` (see ``setup.cfg``),
which is exactly where the loci script looks.

Source layout
-------------

::

    cyborg_dashboard/
      api/cyborg.py                  - thin Cyborg REST client
      content/admin/accelerator/     - panel, tables, views, urls
      enabled/                       - panel group + panel registration
      templates/admin/accelerator/   - panel template

License
-------

Apache License 2.0. See ``LICENSE``.
